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

package mount_test

import (
	"fmt"
	"testing"
	"time"

	gomock "github.com/golang/mock/gomock"
	mocks "github.com/ibm/ibm-block-csi-driver/node/mocks"
	"github.com/ibm/ibm-block-csi-driver/node/pkg/driver/mount"
)

func TestMounterUnmount(t *testing.T) {
	target := "fake/target"
	output := []byte("fake output")

	// default timeout is set to 30s
	timeout := 30 * time.Second

	testCases := []struct {
		name      string
		execErr   error
		expReturn error
	}{
		{
			name:      "unmount without error",
			execErr:   nil,
			expReturn: nil,
		},
		{
			name:      "unmount timeout",
			execErr:   fmt.Errorf("timeout"),
			expReturn: fmt.Errorf("Unmount failed: %v\nUnmounting arguments: %s\nOutput: %s\n", fmt.Errorf("timeout"), target, string(output)),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {

			mockCtrl := gomock.NewController(t)
			defer mockCtrl.Finish()

			fake_executer := mocks.NewMockExecuterInterface(mockCtrl)
			fake_executer.EXPECT().
				ExecuteWithTimeout(int(timeout.Seconds()*1000), "umount", []string{target}).
				Return(output, tc.execErr)

			mounter := mount.NewWithExecutor("", fake_executer)
			err := mounter.Unmount(target)

			if tc.expReturn == nil {
				if err != nil {
					t.Fatalf("Expected return %v, got %v", tc.expReturn, err)
				}
			} else if err.Error() != tc.expReturn.Error() {
				t.Fatalf("Expected return %v, got %v", tc.expReturn, err)
			}

		})
	}

}
