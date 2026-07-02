package main

import (
	"encoding/json"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("the recorded Status corpus declaration", func() {
	When("the scenarios name their fixture files", func() {
		It("names every fixture uniquely, so no capture overwrites another", func() {
			seen := make(map[string]bool, len(scenarios))
			for _, scenario := range scenarios {
				Expect(seen).NotTo(HaveKey(scenario.fixture),
					"two scenarios must not share the fixture file %q", scenario.fixture)
				seen[scenario.fixture] = true
			}
		})
	})

	When("the scenarios spell their Manifests", func() {
		It("spells every Manifest as valid JSON, so a capture failure is the server's verdict, not a typo", func() {
			for _, scenario := range scenarios {
				Expect(json.Valid([]byte(scenario.manifest))).To(BeTrue(),
					"the Manifest for %q must be valid JSON", scenario.fixture)
			}
		})

		It("pins the failure each scenario must record, so a capture cannot record the wrong Status", func() {
			for _, scenario := range scenarios {
				Expect(scenario.expect).NotTo(BeEmpty(),
					"the scenario for %q must expect a failure substring", scenario.fixture)
			}
		})
	})

	When("the deny webhook answers an AdmissionReview", func() {
		It("echoes the request UID and denies with the recorded message", func() {
			response := denyResponse("a-request-uid")
			raw, err := json.Marshal(response)
			Expect(err).NotTo(HaveOccurred())

			var review struct {
				APIVersion string `json:"apiVersion"`
				Kind       string `json:"kind"`
				Response   struct {
					UID     string `json:"uid"`
					Allowed bool   `json:"allowed"`
					Status  struct {
						Message string `json:"message"`
					} `json:"status"`
				} `json:"response"`
			}
			Expect(json.Unmarshal(raw, &review)).To(Succeed())
			Expect(review.APIVersion).To(Equal("admission.k8s.io/v1"))
			Expect(review.Kind).To(Equal("AdmissionReview"))
			Expect(review.Response.UID).To(Equal("a-request-uid"))
			Expect(review.Response.Allowed).To(BeFalse())
			Expect(review.Response.Status.Message).To(Equal(denialMessage))
		})
	})
})
