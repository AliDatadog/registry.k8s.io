#!/usr/bin/env bash

# Copyright 2022 The Kubernetes Authors.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

set -o errexit -o nounset -o pipefail

# cd to self
cd "$(dirname "${BASH_SOURCE[0]}")"

# fetch data for each supported cloud
curl -Lo 'data/aws-ip-ranges.json' 'https://ip-ranges.amazonaws.com/ip-ranges.json'
curl -Lo 'data/gcp-cloud.json' 'https://www.gstatic.com/ipranges/cloud.json'
curl -Lo 'data/azure-cloud.json' 'https://download.microsoft.com/download/7/1/D/71D86715-5596-4529-9B13-DA13A5DE5B63/ServiceTags_Public_20241021.json'