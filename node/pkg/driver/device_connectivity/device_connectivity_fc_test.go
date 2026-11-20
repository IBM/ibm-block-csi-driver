/**
 * Copyright 2026 IBM Corp.
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

package device_connectivity_test

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/ibm/ibm-block-csi-driver/node/mocks"
	"github.com/ibm/ibm-block-csi-driver/node/pkg/driver/device_connectivity"
)

const (
	fcVolumeId        = "600507680c00000000000000000000aa"
	fcAllowLunScan    = "/sys/module/zfcp/parameters/allow_lun_scan"
	fcLszfcpTimeout   = 5 * 1000
	fcNpivOutput      = "0.0.1800 host0 ...\n    port_type: NPIV VPORT\n"
	fcNportOutput     = "0.0.1800 host0 ...\n    port_type: NPort (fabric via point-to-point)\n"
	fcOriginalMessage = "Couldn't find multipath device for volumeID [" + fcVolumeId + "]. Please check the host connectivity to the storage."
	fcLunScanIssue    = `zfcp allow_lun_scan is "N"`
	fcNpivIssue       = `FCP adapter port_type is "NPort (fabric via point-to-point)"`
)

type fcFixture struct {
	fc     *device_connectivity.OsDeviceConnectivityFc
	exec   *mocks.MockExecuterInterface
	helper *mocks.MockOsDeviceConnectivityHelperScsiGenericInterface
}

func newFcFixture(t *testing.T) *fcFixture {
	t.Helper()
	ctrl := gomock.NewController(t)
	exec := mocks.NewMockExecuterInterface(ctrl)
	helper := mocks.NewMockOsDeviceConnectivityHelperScsiGenericInterface(ctrl)
	return &fcFixture{
		fc:     &device_connectivity.OsDeviceConnectivityFc{Executer: exec, HelperScsiGeneric: helper},
		exec:   exec,
		helper: helper,
	}
}

func TestFcGetMpathDeviceZfcpDiagnostics(t *testing.T) {
	testCases := []struct {
		name           string
		allowLunScan   []byte
		allowLunErr    error
		lszfcpOut      string
		lszfcpErr      error
		wantContains   []string
		wantNotContain []string
		wantOriginal   bool
	}{
		{
			name:         "healthy zFCP config keeps the standard error message",
			allowLunScan: []byte("Y\n"),
			lszfcpOut:    fcNpivOutput,
			wantOriginal: true,
		},
		{
			name:           "allow_lun_scan N reports LUN scan issue only",
			allowLunScan:   []byte("N\n"),
			lszfcpOut:      fcNpivOutput,
			wantContains:   []string{fcLunScanIssue, "zfcp.allow_lun_scan=1"},
			wantNotContain: []string{fcNpivIssue},
		},
		{
			name:           "NPort without NPIV reports NPIV issue only",
			allowLunScan:   []byte("Y\n"),
			lszfcpOut:      fcNportOutput,
			wantContains:   []string{fcNpivIssue, "NPIV VPORT"},
			wantNotContain: []string{fcLunScanIssue},
		},
		{
			name:         "allow_lun_scan N and NPort report both issues",
			allowLunScan: []byte("N\n"),
			lszfcpOut:    fcNportOutput,
			wantContains: []string{fcLunScanIssue, fcNpivIssue},
		},
		{
			name:         "non zFCP host (no zfcp module parameter) is skipped silently",
			allowLunErr:  os.ErrNotExist,
			wantOriginal: true,
		},
		{
			name:         "lszfcp failure keeps the standard error message",
			allowLunScan: []byte("Y\n"),
			lszfcpErr:    errors.New("lszfcp: command not found"),
			wantOriginal: true,
		},
		{
			name:         "lszfcp empty output keeps the standard error message",
			allowLunScan: []byte("Y\n"),
			lszfcpOut:    "",
			wantOriginal: true,
		},
		{
			name:         "unreadable allow_lun_scan still runs NPIV check",
			allowLunErr:  errors.New("permission denied"),
			lszfcpOut:    fcNpivOutput,
			wantOriginal: true,
		},
		{
			name:         "mixed NPIV and NPort ports are not flagged",
			allowLunScan: []byte("Y\n"),
			lszfcpOut:    fcNportOutput + "    port_type: NPIV VPORT\n",
			wantOriginal: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			fx := newFcFixture(t)
			notFound := &device_connectivity.MultipathDeviceNotFoundForVolumeError{VolumeId: fcVolumeId}
			fx.helper.EXPECT().GetMpathDevice(gomock.Any(), fcVolumeId).Return("", notFound)
			fx.exec.EXPECT().IoutilReadFile(fcAllowLunScan).Return(tc.allowLunScan, tc.allowLunErr).Times(1)
			if !errors.Is(tc.allowLunErr, os.ErrNotExist) {
				fx.exec.EXPECT().ExecuteWithTimeout(fcLszfcpTimeout, "lszfcp", []string{"-Ha"}).
					Return([]byte(tc.lszfcpOut), tc.lszfcpErr).Times(1)
			}

			_, err := fx.fc.GetMpathDevice(context.Background(), fcVolumeId)
			if err == nil {
				t.Fatal("expected an error")
			}

			var got *device_connectivity.MultipathDeviceNotFoundForVolumeError
			if !errors.As(err, &got) {
				t.Fatalf("expected MultipathDeviceNotFoundForVolumeError, got %T: %v", err, err)
			}

			msg := err.Error()
			if !strings.HasPrefix(msg, "Couldn't find multipath device for volumeID ["+fcVolumeId+"]") {
				t.Fatalf("original error prefix not preserved: %q", msg)
			}
			if tc.wantOriginal && msg != fcOriginalMessage {
				t.Fatalf("expected original message %q, got %q", fcOriginalMessage, msg)
			}
			for _, want := range tc.wantContains {
				if !strings.Contains(msg, want) {
					t.Errorf("expected message to contain %q, got %q", want, msg)
				}
			}
			for _, notWant := range tc.wantNotContain {
				if strings.Contains(msg, notWant) {
					t.Errorf("expected message not to contain %q, got %q", notWant, msg)
				}
			}
		})
	}
}

func TestFcGetMpathDeviceSuccessIsUnaffected(t *testing.T) {
	fx := newFcFixture(t)
	fx.helper.EXPECT().GetMpathDevice(gomock.Any(), fcVolumeId).Return("dm-3", nil)

	dm, err := fx.fc.GetMpathDevice(context.Background(), fcVolumeId)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dm != "dm-3" {
		t.Fatalf("expected dm-3, got %q", dm)
	}
}

func TestFcGetMpathDeviceOtherErrorIsUnchanged(t *testing.T) {
	fx := newFcFixture(t)
	discoveryErr := errors.New("some other discovery failure")
	fx.helper.EXPECT().GetMpathDevice(gomock.Any(), fcVolumeId).Return("", discoveryErr)

	_, err := fx.fc.GetMpathDevice(context.Background(), fcVolumeId)
	if err != discoveryErr {
		t.Fatalf("expected original error to be returned unchanged, got %v", err)
	}
}
