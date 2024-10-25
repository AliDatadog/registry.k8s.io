/*
Copyright 2022 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package cloudcidrs

import (
	"net/netip"
	"os"
	"path/filepath"

	"k8s.io/registry.k8s.io/pkg/net/cidrs"
)

// AWS cloud
const AWS = "AWS"

// GCP cloud
const GCP = "GCP"

// Azure cloud
const AZ = "AZ"

type regionPrefixMapper map[string][]netip.Prefix

// NewIPMapper returns cidrs.IPMapper populated with cloud region info
// for the clouds we have resources for, currently GCP and AWS
func NewIPMapper() cidrs.IPMapper[IPInfo] {
	t := cidrs.NewTrieMap[IPInfo]()
	dataDir := os.Getenv("DATA_DIR")
	if dataDir == "" {
		dataDir = "./data"
	}
	// read in data
	awsRaw := mustReadFile(filepath.Join(dataDir, "aws-ip-ranges.json"))
	gcpRaw := mustReadFile(filepath.Join(dataDir, "gcp-cloud.json"))
	azRaw := mustReadFile(filepath.Join(dataDir, "azure-cloud.json"))
	// parse raw AWS IP range data
	awsRTP, err := parseAWS(awsRaw)
	if err != nil {
		panic(err)
	}
	// parse GCP IP range data
	gcpRTP, err := parseGCP(gcpRaw)
	if err != nil {
		panic(err)
	}
	azRTP, err := parseAZ(azRaw)
	if err != nil {
		panic(err)
	}

	for region, prefixes := range awsRTP {
		for _, prefix := range prefixes {
			t.Insert(prefix, IPInfo{Region: region, Cloud: AWS})
		}
	}
	for region, prefixes := range gcpRTP {
		for _, prefix := range prefixes {
			t.Insert(prefix, IPInfo{Region: region, Cloud: GCP})
		}
	}
	for region, prefixes := range azRTP {
		for _, prefix := range prefixes {
			t.Insert(prefix, IPInfo{Region: region, Cloud: AZ})
		}
	}
	return t
}

// AllIPInfos returns a slice of all known results that a NewIPMapper could
// return for testing purposes
func AllIPInfos() []IPInfo {
	dataDir := os.Getenv("DATA_DIR")
	if dataDir == "" {
		dataDir = "./data"
	}

	// read in data
	awsRaw := mustReadFile(filepath.Join(dataDir, "aws-ip-ranges.json"))
	gcpRaw := mustReadFile(filepath.Join(dataDir, "gcp-cloud.json"))
	azRaw := mustReadFile(filepath.Join(dataDir, "azure-cloud.json"))
	// parse raw AWS IP range data
	awsRTP, err := parseAWS(awsRaw)
	if err != nil {
		panic(err)
	}
	// parse GCP IP range data
	gcpRTP, err := parseGCP(gcpRaw)
	if err != nil {
		panic(err)
	}
	azRTP, err := parseAZ(azRaw)
	if err != nil {
		panic(err)
	}

	var allIPInfos []IPInfo
	for region := range awsRTP {
		allIPInfos = append(allIPInfos, IPInfo{Region: region, Cloud: AWS})
	}
	for region := range gcpRTP {
		allIPInfos = append(allIPInfos, IPInfo{Region: region, Cloud: GCP})
	}
	for region := range azRTP {
		allIPInfos = append(allIPInfos, IPInfo{Region: region, Cloud: AZ})
	}
	return allIPInfos
}

func mustReadFile(filePath string) string {
	contents, err := os.ReadFile(filePath)
	if err != nil {
		panic(err)
	}
	return string(contents)
}
