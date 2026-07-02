package main

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("the fixture corpus naming", func() {
	When("a group-version path from the live index names a fixture file", func() {
		DescribeTable(
			"the sanitized filename replaces path separators with underscores",
			func(groupVersionPath, fileName string) {
				Expect(fixtureFileName(groupVersionPath)).To(Equal(fileName))
			},
			Entry("the core group", "api/v1", "api_v1.json"),
			Entry("the workloads group", "apis/apps/v1", "apis_apps_v1.json"),
			Entry("the group with the JSONSchemaProps $ref cycle",
				"apis/apiextensions.k8s.io/v1", "apis_apiextensions.k8s.io_v1.json"),
			Entry("a sample-CRD group version",
				"apis/craft.example.com/v2", "apis_craft.example.com_v2.json"),
		)
	})

	When("the declared corpus is mapped to fixture files", func() {
		It("names every group-version uniquely, so no capture overwrites another", func() {
			seen := make(map[string]string, len(corpusGroupVersions))
			for _, groupVersionPath := range corpusGroupVersions {
				name := fixtureFileName(groupVersionPath)
				Expect(seen).NotTo(HaveKey(name),
					"%q and %q must not share the fixture file %q",
					seen[name], groupVersionPath, name)
				seen[name] = groupVersionPath
			}
		})
	})
})
