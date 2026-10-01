/**
 * Copyright 2019 IBM Corp.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

// Internal (white-box) tests for unexported NVMe-oFC path-accounting helpers that need
// no Executer mock — kept in package device_connectivity to avoid the mocks import cycle.
package device_connectivity

import "testing"

func TestNormalizeTraddr(t *testing.T) {
	cases := map[string]string{
		"nn-0x500507681000e48b:pn-0x500507681019e48b": "nn-500507681000e48b:pn-500507681019e48b",
		"nn-500507681000e48b:pn-500507681019e48b":     "nn-500507681000e48b:pn-500507681019e48b",
		"NN-0X500507681000E48B":                       "nn-500507681000e48b",
		"  nn-0x50:pn-0x60  ":                         "nn-50:pn-60",
	}
	for in, want := range cases {
		if got := normalizeTraddr(in); got != want {
			t.Fatalf("normalizeTraddr(%q) = %q, want %q", in, got, want)
		}
	}
}

// Reproduces the canary mismatch: the kernel's list-subsys keys carry a "0x" prefix,
// the publish-context array targets do not. Before normalization the count was always 0
// despite live paths; it must now match.
func TestCountLivePathsForSubsystem_0xPrefixMismatch(t *testing.T) {
	livePaths := map[string]bool{
		"nn-0x500507681000e48b:pn-0x500507681019e48b|nn-0x20007ca62a951ce7:pn-0x10007ca62a951ce7": true,
		"nn-0x500507681000e48b:pn-0x50050768101be48b|nn-0x20007ca62a951ce7:pn-0x10007ca62a951ce7": true,
		"nn-0x500507681000e49d:pn-0x500507681029e49d|nn-0x20007ca62a951ce8:pn-0x10007ca62a951ce8": true,
		"nn-0xdeadbeefdeadbeef:pn-0xdeadbeefdeadbeef|nn-0x20007ca62a951ce8:pn-0x10007ca62a951ce8": true, // unrelated subsystem
	}
	arrayTargets := map[string][]string{
		"nn-500507681000e48b:pn-500507681019e48b": nil,
		"nn-500507681000e48b:pn-50050768101be48b": nil,
		"nn-500507681000e49d:pn-500507681029e49d": nil,
	}
	if got := countLivePathsForSubsystem(livePaths, arrayTargets); got != 3 {
		t.Fatalf("expected 3 matching live paths (unrelated subsystem excluded), got %d", got)
	}

	// No array targets in context → no matches.
	if got := countLivePathsForSubsystem(livePaths, map[string][]string{}); got != 0 {
		t.Fatalf("expected 0 with empty array targets, got %d", got)
	}
}
