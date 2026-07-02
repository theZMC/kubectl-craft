package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"log"
	"math/big"
	"net"
	"net/http"
	"time"

	"github.com/testcontainers/testcontainers-go"
)

// denialMessage is the always-deny admission server's verdict; the API
// server wraps it as `admission webhook "deny.craft.example.com" denied the
// request: <message>`, the freeform denial shape the corpus records.
const denialMessage = "the kubectl-craft capture webhook denies every matching Manifest"

// denyWebhook is the in-process always-deny admission server: an HTTPS
// endpoint on the host's loopback that testcontainers' host port access
// makes reachable from inside the cluster as host.testcontainers.internal.
type denyWebhook struct {
	// port is the loopback port the server listens on — the same port the
	// cluster reaches through the host gateway.
	port int
	// certificatePEM is the server's self-signed certificate, doubling as
	// the webhook configuration's caBundle.
	certificatePEM []byte
	server         *http.Server
}

// startDenyWebhook generates a self-signed serving certificate for the host
// gateway name, binds a loopback listener, and serves denials until closed.
func startDenyWebhook() (*denyWebhook, error) {
	certificate, certificatePEM, err := selfSignedCertificate(testcontainers.HostInternal)
	if err != nil {
		return nil, err
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("binding the deny webhook listener: %w", err)
	}

	webhook := &denyWebhook{
		port:           listener.Addr().(*net.TCPAddr).Port,
		certificatePEM: certificatePEM,
		server: &http.Server{
			Handler:           http.HandlerFunc(denyHandler),
			TLSConfig:         &tls.Config{Certificates: []tls.Certificate{certificate}, MinVersion: tls.VersionTLS12},
			ReadHeaderTimeout: 10 * time.Second,
		},
	}
	go func() {
		if serveErr := webhook.server.ServeTLS(listener, "", ""); !errors.Is(serveErr, http.ErrServerClosed) {
			log.Printf("capture-status-fixtures: the deny webhook server stopped: %v", serveErr)
		}
	}()
	return webhook, nil
}

// close stops the admission server; the capture run is done with it.
func (w *denyWebhook) close() {
	if err := w.server.Close(); err != nil {
		log.Printf("capture-status-fixtures: closing the deny webhook server: %v", err)
	}
}

// denyHandler answers every AdmissionReview with allowed=false and the
// denial message, echoing the request UID as the admission contract
// requires.
func denyHandler(writer http.ResponseWriter, request *http.Request) {
	var review struct {
		Request struct {
			UID string `json:"uid"`
		} `json:"request"`
	}
	if err := json.NewDecoder(request.Body).Decode(&review); err != nil {
		http.Error(writer, fmt.Sprintf("decoding the AdmissionReview: %v", err), http.StatusBadRequest)
		return
	}

	writer.Header().Set("Content-Type", "application/json")
	response := denyResponse(review.Request.UID)
	if err := json.NewEncoder(writer).Encode(response); err != nil {
		log.Printf("capture-status-fixtures: writing the denial response: %v", err)
	}
}

// denyResponse spells the always-deny AdmissionReview verdict for one
// request UID.
func denyResponse(uid string) map[string]any {
	return map[string]any{
		"apiVersion": "admission.k8s.io/v1",
		"kind":       "AdmissionReview",
		"response": map[string]any{
			"uid":     uid,
			"allowed": false,
			"status":  map[string]any{"message": denialMessage},
		},
	}
}

// selfSignedCertificate generates a throwaway serving certificate for the
// given DNS name, self-signed so the certificate itself is the CA bundle
// the webhook configuration trusts.
func selfSignedCertificate(host string) (tls.Certificate, []byte, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, nil, fmt.Errorf("generating the webhook key: %w", err)
	}

	template := x509.Certificate{
		SerialNumber:          big.NewInt(time.Now().UnixNano()),
		Subject:               pkix.Name{CommonName: host},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
		DNSNames:              []string{host},
	}
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, nil, fmt.Errorf("self-signing the webhook certificate: %w", err)
	}

	certificatePEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return tls.Certificate{}, nil, fmt.Errorf("encoding the webhook key: %w", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})

	certificate, err := tls.X509KeyPair(certificatePEM, keyPEM)
	if err != nil {
		return tls.Certificate{}, nil, fmt.Errorf("assembling the webhook key pair: %w", err)
	}
	return certificate, certificatePEM, nil
}
