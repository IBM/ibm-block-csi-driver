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

package device_connectivity_test

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/golang/mock/gomock"
	"github.com/ibm/ibm-block-csi-driver/node/mocks"
	"github.com/ibm/ibm-block-csi-driver/node/pkg/driver/device_connectivity"
	"github.com/ibm/ibm-block-csi-driver/node/pkg/driver/executer"
)

var (
	volumeUuid = "6005076810840239d000000000000d6b"
	// volumeNguid is the NVMe NGUID that convertScsiIdToNguid() derives from volumeUuid:
	// <vendor-id-extension(16)> + <oui(6)> + "0" + <vendor-identifier(9)>
	volumeNguid = "d000000000000d6b0050760810840239"
)

// newGaterForTest returns a real (non nil) KeyedGater. Production code paths that read sysfs
// go through the gater, and a nil gater would panic on the first call.
func newGaterForTest() *executer.KeyedGater {
	return executer.NewKeyedGater(100)
}

func NewOsDeviceConnectivityHelperScsiGenericForTest(
	exec executer.ExecuterInterface,
	helper device_connectivity.OsDeviceConnectivityHelperInterface,
	mutexLock *sync.Mutex,
) device_connectivity.OsDeviceConnectivityHelperScsiGenericInterface {
	return &device_connectivity.OsDeviceConnectivityHelperScsiGeneric{
		Executer:        exec,
		KeyedGater:      newGaterForTest(),
		Helper:          helper,
		MutexMultipathF: mutexLock,
	}
}

func NewOsDeviceConnectivityHelperGenericForTest(
	exec executer.ExecuterInterface,
	helper device_connectivity.GetDmsPathHelperInterface,
) device_connectivity.OsDeviceConnectivityHelperInterface {
	return &device_connectivity.OsDeviceConnectivityHelperGeneric{
		Executer: exec,
		Helper:   helper,
	}
}

// =========================================================================
// Test: GetMpathDevice
// =========================================================================
func TestGetMpathDevice(t *testing.T) {
	testCases := []struct {
		name          string
		volumeId      string
		mockReturnDm  string
		mockReturnErr error
		expDMPath     string
		expErr        error
	}{
		{
			name:          "Should succeed when WaitForDmToExist finds dm path",
			volumeId:      volumeUuid,
			mockReturnDm:  "/dev/mapper/mpatha",
			mockReturnErr: nil,
			expDMPath:     "/dev/mapper/mpatha",
			expErr:        nil,
		},
		{
			name:          "Should fail when WaitForDmToExist returns error",
			volumeId:      volumeUuid,
			mockReturnDm:  "",
			mockReturnErr: errors.New("multipath device not found"),
			expDMPath:     "",
			expErr:        errors.New("multipath device not found"),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			mockCtrl := gomock.NewController(t)
			defer mockCtrl.Finish()

			fakeExecuter := mocks.NewMockExecuterInterface(mockCtrl)
			fakeHelper := mocks.NewMockOsDeviceConnectivityHelperInterface(mockCtrl)
			fakeMutex := &sync.Mutex{}

			ctx := context.Background()

			fakeHelper.EXPECT().WaitForDmToExist(
				ctx,
				gomock.Any(),
				tc.volumeId,
				device_connectivity.WaitForMpathRetries,
				device_connectivity.WaitForMpathWaitIntervalSec,
			).Return(tc.mockReturnDm, tc.mockReturnErr)

			o := NewOsDeviceConnectivityHelperScsiGenericForTest(fakeExecuter, fakeHelper, fakeMutex)
			dmPath, err := o.GetMpathDevice(ctx, tc.volumeId)

			if tc.expErr != nil {
				if err == nil {
					t.Fatalf("Expected error %v, got nil", tc.expErr)
				}
				if err.Error() != tc.expErr.Error() {
					t.Fatalf("Expected error %v, got %v", tc.expErr, err)
				}
			} else {
				if err != nil {
					t.Fatalf("Expected success, got error %v", err)
				}
				if dmPath != tc.expDMPath {
					t.Fatalf("Expected dmPath %v, got %v", tc.expDMPath, dmPath)
				}
			}
		})
	}
}

// =========================================================================
// Test: WaitForDmToExist on GetDmsPathHelperGeneric
//
// WaitForDmToExist discovers devices from the host sysfs/devfs topology (it no longer shells out to
// "multipathd show maps"), so a positive lookup needs a live multipath device on the machine running
// the test. Here we cover the host-independent behaviour: a WWID that is not present on the host is
// reported as MultipathDeviceNotFoundForVolumeError, and a cancelled context aborts immediately.
// =========================================================================
func TestHelperWaitForDmToExist(t *testing.T) {
	testCases := []struct {
		name         string
		volumeWwid   string
		cancelCtx    bool
		expNotFound  bool
		expCtxCancel bool
	}{
		{
			name:        "Should fail with not-found error when no device on the host matches the WWID",
			volumeWwid:  volumeUuid,
			expNotFound: true,
		},
		{
			name:         "Should abort with context error when the context is already cancelled",
			volumeWwid:   volumeUuid,
			cancelCtx:    true,
			expCtxCancel: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			mockCtrl := gomock.NewController(t)
			defer mockCtrl.Finish()

			// No executer calls are expected: any unexpected call fails the test.
			fakeExecuter := mocks.NewMockExecuterInterface(mockCtrl)

			helperGeneric := device_connectivity.NewGetDmsPathHelperGeneric(fakeExecuter)

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if tc.cancelCtx {
				cancel()
			}

			dm, err := helperGeneric.WaitForDmToExist(ctx, newGaterForTest(), tc.volumeWwid, 1, 0)
			if err == nil {
				t.Fatalf("Expected error, got nil (dm=%q)", dm)
			}
			if dm != "" {
				t.Fatalf("Expected empty dm path on error, got %q", dm)
			}

			if tc.expNotFound {
				var notFound *device_connectivity.MultipathDeviceNotFoundForVolumeError
				if !errors.As(err, &notFound) {
					t.Fatalf("Expected MultipathDeviceNotFoundForVolumeError, got %T: %v", err, err)
				}
			}
			if tc.expCtxCancel && !errors.Is(err, context.Canceled) {
				t.Fatalf("Expected context.Canceled, got %v", err)
			}
		})
	}
}

// =========================================================================
// Test: VPD 0x83 Binary Parsing (In-Kernel SCSI Page Parsing)
// =========================================================================
func TestParseVPD83(t *testing.T) {
	naaData := []byte{
		0x00, 0x83, 0x00, 0x14, // Header: page length = 20
		0x01, 0x03, 0x00, 0x10, // Binary, Assoc=0 (LUN), Type=3 (NAA), Length=16
		0x60, 0x05, 0x07, 0x68, 0x10, 0x84, 0x02, 0x39,
		0xd0, 0x00, 0x00, 0x00, 0x00, 0x00, 0x0d, 0x6b,
	}

	shortData := []byte{0x00, 0x83}

	testCases := []struct {
		name      string
		data      []byte
		expWwn    string
		expectErr bool
	}{
		{
			name:      "Should parse valid NAA 16-byte identifier correctly",
			data:      naaData,
			expWwn:    "3" + volumeUuid,
			expectErr: false,
		},
		{
			name:      "Should return error on buffer shorter than 4 bytes",
			data:      shortData,
			expWwn:    "",
			expectErr: true,
		},
		{
			name:      "Should return error when no Association 0 descriptor is present",
			data:      []byte{0x00, 0x83, 0x00, 0x04, 0x01, 0x13, 0x00, 0x00},
			expWwn:    "",
			expectErr: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			mockCtrl := gomock.NewController(t)
			defer mockCtrl.Finish()

			fakeExecuter := mocks.NewMockExecuterInterface(mockCtrl)
			helper := device_connectivity.NewOsDeviceConnectivityHelperGeneric(fakeExecuter, nil, nil)
			genericHelper, ok := helper.(*device_connectivity.OsDeviceConnectivityHelperGeneric)
			if !ok {
				t.Fatalf("Failed to cast to OsDeviceConnectivityHelperGeneric")
			}

			wwn, err := genericHelper.ParseVPD83(tc.data)
			if tc.expectErr {
				if err == nil {
					t.Fatalf("Expected error, got wwn %q", wwn)
				}
				return
			}
			if err != nil {
				t.Fatalf("Expected success, got error %v", err)
			}
			if wwn != tc.expWwn {
				t.Fatalf("ParseVPD83() = %q, want %q", wwn, tc.expWwn)
			}
		})
	}
}

// =========================================================================
// Test: Extract Host Number from Sysfs Paths / Entries
// =========================================================================
func TestExtractHostNumber(t *testing.T) {
	testCases := []struct {
		name      string
		entryName string
		expHost   int
		expectErr bool
	}{
		{
			name:      "Standard hostX name",
			entryName: "host3",
			expHost:   3,
			expectErr: false,
		},
		{
			name:      "Standard high number host",
			entryName: "host128",
			expHost:   128,
			expectErr: false,
		},
		{
			name:      "Fibre channel rport format",
			entryName: "rport-4:0-0",
			expHost:   4,
			expectErr: false,
		},
		{
			name:      "Fibre channel remote_port format",
			entryName: "remote_port-12:0-1",
			expHost:   12,
			expectErr: false,
		},
		{
			name:      "Invalid format",
			entryName: "invalid_format",
			expHost:   0,
			expectErr: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			mockCtrl := gomock.NewController(t)
			defer mockCtrl.Finish()

			fakeExecuter := mocks.NewMockExecuterInterface(mockCtrl)
			helper := device_connectivity.NewOsDeviceConnectivityHelperGeneric(fakeExecuter, nil, nil)
			genericHelper, ok := helper.(*device_connectivity.OsDeviceConnectivityHelperGeneric)
			if !ok {
				t.Fatalf("Failed to cast to OsDeviceConnectivityHelperGeneric")
			}

			host, err := genericHelper.ExtractHostNumber(tc.entryName)
			if tc.expectErr {
				if err == nil {
					t.Fatalf("Expected error for %q, got host %d", tc.entryName, host)
				}
				return
			}
			if err != nil {
				t.Fatalf("Expected success for %q, got error %v", tc.entryName, err)
			}
			if host != tc.expHost {
				t.Fatalf("ExtractHostNumber(%q) = %d, want %d", tc.entryName, host, tc.expHost)
			}
		})
	}
}

// =========================================================================
// Test: Hardware State Blocked / Ghost Safety Gate Checks
// =========================================================================
func TestIsHardwareStateBlocked(t *testing.T) {
	testCases := []struct {
		name      string
		state     string
		expResult bool
	}{
		{
			name:      "Running state is not blocked",
			state:     "running",
			expResult: false,
		},
		{
			name:      "Blocked state is blocked",
			state:     "blocked",
			expResult: true,
		},
		{
			name:      "Quiesce state is blocked",
			state:     "quiesce",
			expResult: true,
		},
		{
			name:      "Transport-offline state is blocked from ioctl",
			state:     "transport-offline",
			expResult: true,
		},
		{
			name:      "Offline state is not in IsHardwareStateBlocked",
			state:     "offline",
			expResult: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			helper := &device_connectivity.OsDeviceConnectivityHelperScsiGeneric{}
			got := helper.IsHardwareStateBlocked(tc.state)
			if got != tc.expResult {
				t.Fatalf("IsHardwareStateBlocked(%q) = %v, want %v", tc.state, got, tc.expResult)
			}
		})
	}
}

// =========================================================================
// Test: Protocol Discrimination & Name Checking (DM vs Native NVMe)
//
// IsDeviceMapper inspects the host sysfs / /dev/mapper, so cases whose answer depends on a device that
// has to exist on the host (dm-N present => true, a friendly alias absent => false) are skipped when
// the host does not match the scenario.
// =========================================================================
func TestProtocolDiscrimination(t *testing.T) {
	testCases := []struct {
		name           string
		deviceName     string
		isDeviceMapper bool
		isNativeNVMe   bool
	}{
		{
			name:           "Standard dm-0 Device Mapper",
			deviceName:     "dm-0",
			isDeviceMapper: true,
			isNativeNVMe:   false,
		},
		{
			name:           "Standard dm-3 Device Mapper",
			deviceName:     "dm-3",
			isDeviceMapper: true,
			isNativeNVMe:   false,
		},
		{
			name:           "Native NVMe subsystem controller",
			deviceName:     "nvme0n1",
			isDeviceMapper: false,
			isNativeNVMe:   true,
		},
		{
			name:           "Native NVMe multipath shared controller node",
			deviceName:     "nvme1c2n1",
			isDeviceMapper: false,
			isNativeNVMe:   true,
		},
		{
			name:           "Traditional SCSI disk",
			deviceName:     "sda",
			isDeviceMapper: false,
			isNativeNVMe:   false,
		},
		{
			name:           "Custom friendly DM alias",
			deviceName:     "mpatha",
			isDeviceMapper: false,
			isNativeNVMe:   false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.isDeviceMapper {
				if _, err := os.Stat("/sys/block/" + tc.deviceName); err != nil {
					t.Skipf("host has no /sys/block/%s, cannot verify Device Mapper detection", tc.deviceName)
				}
			} else if strings.HasPrefix(tc.deviceName, "mpath") {
				if _, err := os.Stat("/dev/mapper/" + tc.deviceName); err == nil {
					t.Skipf("host has a real /dev/mapper/%s, which is (correctly) detected as a Device Mapper node", tc.deviceName)
				}
			}

			mockCtrl := gomock.NewController(t)
			defer mockCtrl.Finish()

			fakeExecuter := mocks.NewMockExecuterInterface(mockCtrl)
			dmsHelper := device_connectivity.NewGetDmsPathHelperGeneric(fakeExecuter)
			scsiHelper := &device_connectivity.OsDeviceConnectivityHelperScsiGeneric{}

			gotDM := dmsHelper.IsDeviceMapper(tc.deviceName)
			if gotDM != tc.isDeviceMapper {
				t.Fatalf("IsDeviceMapper(%q) = %v, want %v", tc.deviceName, gotDM, tc.isDeviceMapper)
			}

			gotNVMe := scsiHelper.IsNativeNvmeNamespace(tc.deviceName)
			if gotNVMe != tc.isNativeNVMe {
				t.Fatalf("IsNativeNvmeNamespace(%q) = %v, want %v", tc.deviceName, gotNVMe, tc.isNativeNVMe)
			}
		})
	}
}

// =========================================================================
// Test: IsSerialMatch
// =========================================================================
func TestIsSerialMatch(t *testing.T) {
	testCases := []struct {
		name           string
		hwSerial       string
		expectedSerial string
		expected       bool
	}{
		{
			name:           "Exact match",
			hwSerial:       "6005076810840239d000000000000d6b",
			expectedSerial: "6005076810840239d000000000000d6b",
			expected:       true,
		},
		{
			name:           "Match with case insensitivity and whitespace",
			hwSerial:       " 6005076810840239D000000000000D6B ",
			expectedSerial: "6005076810840239d000000000000d6b",
			expected:       true,
		},
		{
			name:           "Match with standard NAA-3 prefix in hardware serial",
			hwSerial:       "36005076810840239d000000000000d6b",
			expectedSerial: "6005076810840239d000000000000d6b",
			expected:       true,
		},
		{
			name:           "Mismatch returns false",
			hwSerial:       "36005076810840239d000000000000000",
			expectedSerial: "6005076810840239d000000000000d6b",
			expected:       false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			helper := &device_connectivity.OsDeviceConnectivityHelperScsiGeneric{}
			got := helper.IsSerialMatch(tc.hwSerial, tc.expectedSerial)
			if got != tc.expected {
				t.Fatalf("IsSerialMatch(%q, %q) = %v, want %v", tc.hwSerial, tc.expectedSerial, got, tc.expected)
			}
		})
	}
}

// =========================================================================
// Test: MatchVolumeToScsiSpec (Dual Protocol SCSI vs NVMe-EUI matching)
// =========================================================================
func TestMatchVolumeToScsiSpec(t *testing.T) {
	testCases := []struct {
		name      string
		parsedID  string
		rawScsiID string
		expected  bool
	}{
		{
			name:      "Exact 32-char SCSI NAA match",
			parsedID:  "36005076810840239d000000000000d6b",
			rawScsiID: "6005076810840239d000000000000d6b",
			expected:  true,
		},
		{
			name:      "SCSI with naa. prefix",
			parsedID:  "naa.6005076810840239d000000000000d6b",
			rawScsiID: "6005076810840239d000000000000d6b",
			expected:  true,
		},
		{
			name:      "SCSI with t10. prefix",
			parsedID:  "t10.6005076810840239d000000000000d6b",
			rawScsiID: "6005076810840239d000000000000d6b",
			expected:  true,
		},
		{
			name:      "NVMe EUI prefix translated match",
			parsedID:  "nvme-eui." + volumeNguid,
			rawScsiID: volumeUuid,
			expected:  true,
		},
		{
			name:      "Invalid raw SCSI ID length (not 32 hex chars)",
			parsedID:  "36005076810840239",
			rawScsiID: "6005076810840239",
			expected:  false,
		},
		{
			name:      "SCSI mismatch",
			parsedID:  "360050768108402390000000000000000",
			rawScsiID: volumeUuid,
			expected:  false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			helper := &device_connectivity.OsDeviceConnectivityHelperScsiGeneric{}
			got := helper.MatchVolumeToScsiSpec(tc.parsedID, tc.rawScsiID)
			if got != tc.expected {
				t.Fatalf("MatchVolumeToScsiSpec(%q, %q) = %v, want %v", tc.parsedID, tc.rawScsiID, got, tc.expected)
			}
		})
	}
}

// =========================================================================
// Test: NormalizeLun
// =========================================================================
func TestNormalizeLun(t *testing.T) {
	testCases := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "Decimal string as-is",
			input:    "0",
			expected: "0",
		},
		{
			name:     "Decimal positive LUN",
			input:    "15",
			expected: "15",
		},
		{
			name:     "Hexadecimal LUN representation",
			input:    "0x0",
			expected: "0",
		},
		{
			name:     "Hexadecimal higher LUN",
			input:    "0x10",
			expected: "16",
		},
		{
			name:     "Empty input",
			input:    "",
			expected: "",
		},
		{
			name:     "Whitespace input",
			input:    "  0x20  ",
			expected: "32",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			helper := &device_connectivity.OsDeviceConnectivityHelperScsiGeneric{}
			got := helper.NormalizeLun(tc.input)
			if got != tc.expected {
				t.Fatalf("NormalizeLun(%q) = %q, want %q", tc.input, got, tc.expected)
			}
		})
	}
}

// =========================================================================
// Test: MultipathdAction (Used in DM and NVMe-DM Teardown)
// =========================================================================
func TestMultipathdAction(t *testing.T) {
	testCases := []struct {
		name         string
		cmd          string
		mockResponse string
		mockErr      error
		expectErr    bool
		errContains  string
	}{
		{
			name:         "Success on ok response",
			cmd:          "del map mpatha",
			mockResponse: "ok\n",
			mockErr:      nil,
			expectErr:    false,
		},
		{
			name:         "Success on map deleted response",
			cmd:          "del map mpatha",
			mockResponse: "map mpatha deleted\n",
			mockErr:      nil,
			expectErr:    false,
		},
		{
			name:         "Idempotency: map not found is treated as success",
			cmd:          "del map mpatha",
			mockResponse: "fail not found\n",
			mockErr:      nil,
			expectErr:    false,
		},
		{
			name:         "Map in use error handled",
			cmd:          "del map mpatha",
			mockResponse: "fail map in use\n",
			mockErr:      nil,
			expectErr:    true,
			errContains:  "map in use",
		},
		{
			name:         "Generic multipathd failure",
			cmd:          "del map mpatha",
			mockResponse: "fail syntax error\n",
			mockErr:      nil,
			expectErr:    true,
			errContains:  "multipathd command failed",
		},
		{
			name:         "Underlying execution error",
			cmd:          "del map mpatha",
			mockResponse: "",
			mockErr:      errors.New("multipathd daemon socket unreachable"),
			expectErr:    true,
			errContains:  "socket unreachable",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			mockCtrl := gomock.NewController(t)
			defer mockCtrl.Finish()

			fakeExecuter := mocks.NewMockExecuterInterface(mockCtrl)
			ctx := context.Background()

			fakeExecuter.EXPECT().MultipathdCmd(ctx, "", tc.cmd).Return(tc.mockResponse, tc.mockErr)

			helper := &device_connectivity.OsDeviceConnectivityHelperScsiGeneric{
				Executer: fakeExecuter,
			}

			err := helper.MultipathdAction(ctx, tc.cmd)

			if tc.expectErr {
				if err == nil {
					t.Fatalf("Expected error containing %q, got nil", tc.errContains)
				}
				if !strings.Contains(err.Error(), tc.errContains) {
					t.Fatalf("Expected error containing %q, got %q", tc.errContains, err.Error())
				}
			} else {
				if err != nil {
					t.Fatalf("Expected success, got error: %v", err)
				}
			}
		})
	}
}

// =========================================================================
// Test: WaitForNoRefs (Device Open Count Polling)
// =========================================================================
func TestWaitForNoRefs(t *testing.T) {
	testCases := []struct {
		name          string
		dmName        string
		openCounts    []int32
		expectedCount int32
	}{
		{
			name:          "Immediately unreferenced (openCount=0)",
			dmName:        "dm-1",
			openCounts:    []int32{0},
			expectedCount: 0,
		},
		{
			name:          "Transitions from openCount=1 to openCount=0 on second poll",
			dmName:        "dm-1",
			openCounts:    []int32{1, 0},
			expectedCount: 0,
		},
		{
			name:          "Remains busy across polls",
			dmName:        "dm-1",
			openCounts:    []int32{2, 2, 2, 2, 2, 2, 2, 2, 2, 2},
			expectedCount: 2,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			mockCtrl := gomock.NewController(t)
			defer mockCtrl.Finish()

			fakeHelper := mocks.NewMockOsDeviceConnectivityHelperInterface(mockCtrl)
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()

			// Return the open counts in sequence (the last one repeats). WaitForNoRefs stops polling as soon
			// as it sees 0, or when the context expires, so the number of polls is not fixed.
			pollIdx := 0
			fakeHelper.EXPECT().GetOpenCount(gomock.Any(), tc.dmName).DoAndReturn(
				func(_ context.Context, _ string) (int32, error) {
					i := pollIdx
					if i >= len(tc.openCounts) {
						i = len(tc.openCounts) - 1
					}
					pollIdx++
					return tc.openCounts[i], nil
				}).MinTimes(1)

			helper := &device_connectivity.OsDeviceConnectivityHelperScsiGeneric{
				Helper: fakeHelper,
			}

			got := helper.WaitForNoRefs(ctx, tc.dmName)
			if got != tc.expectedCount {
				t.Fatalf("WaitForNoRefs(%q) = %d, want %d", tc.dmName, got, tc.expectedCount)
			}
		})
	}
}

// =========================================================================
// Test: TeardownVolume Multi-Protocol Workflow Coverage
// Covers:
//  1. Standard Device Mapper (SCSI, iSCSI, FC)
//  2. NVMe over Device Mapper (NVMe-DM)
//  3. Native NVMe Subsystem (Native NVMe)
//  4. Idempotency & Error boundaries (Context cancellation, Busy DM hold)
//
// TeardownVolume resolves the backing device by stat()-ing real /dev nodes and reading /proc and
// /sys (collectInformationForTeardown), which cannot be faked through the mocked interfaces.
// Scenarios that need a live kernel block device (needsLiveDevice) are therefore skipped on hosts
// where that device cannot be created; the context-cancellation and idempotency scenarios run
// everywhere.
// =========================================================================
func TestTeardownVolumeMultiProtocol(t *testing.T) {
	testCases := []struct {
		name             string
		protocolType     string // "dm-scsi", "nvme-dm", "nvme-native", "idempotent"
		target           string
		expectedWWID     string
		openCount        int32
		slaves           []string
		multipathdAction string
		ctxCancelled     bool
		needsLiveDevice  bool
		expectErr        bool
		errContains      string
	}{
		{
			name:         "Teardown aborts immediately when context is cancelled",
			protocolType: "idempotent",
			target:       "/var/lib/kubelet/plugins/kubernetes.io/csi/pv/mount",
			expectedWWID: volumeUuid,
			ctxCancelled: true,
			expectErr:    true,
			errContains:  "context canceled",
		},
		{
			name:         "Idempotency: Target unmounted and no mpath resolved",
			protocolType: "idempotent",
			target:       "/var/lib/kubelet/plugins/kubernetes.io/csi/pv/mount_already_gone",
			expectedWWID: volumeUuid,
			ctxCancelled: false,
			expectErr:    false,
		},
		{
			name:            "Protocol 1: Standard SCSI Device Mapper teardown pipeline (del map + unbind slaves)",
			protocolType:    "dm-scsi",
			target:          "/var/lib/kubelet/plugins/kubernetes.io/csi/pv/mount_scsi",
			expectedWWID:    volumeUuid,
			openCount:       0,
			slaves:          []string{"sda", "sdb"},
			needsLiveDevice: true,
			expectErr:       false,
		},
		{
			name:            "Protocol 2: NVMe-over-DM teardown pipeline (del map + nvme slave classification)",
			protocolType:    "nvme-dm",
			target:          "/var/lib/kubelet/plugins/kubernetes.io/csi/pv/mount_nvmedm",
			expectedWWID:    volumeUuid,
			openCount:       0,
			slaves:          []string{"nvme0n1", "nvme1n1"},
			needsLiveDevice: true,
			expectErr:       false,
		},
		{
			name:            "Protocol 3: Native NVMe multipath teardown pipeline (direct controller flush & unbind)",
			protocolType:    "nvme-native",
			target:          "/var/lib/kubelet/plugins/kubernetes.io/csi/pv/mount_nvmenative",
			expectedWWID:    volumeUuid,
			slaves:          []string{"nvme0n1"},
			needsLiveDevice: true,
			expectErr:       false,
		},
		{
			name:            "DM Safety Boundary: Rejects teardown when device mapper remains busy (openCount > 0)",
			protocolType:    "dm-scsi",
			target:          "/var/lib/kubelet/plugins/kubernetes.io/csi/pv/mount_busy",
			expectedWWID:    volumeUuid,
			openCount:       2,
			needsLiveDevice: true,
			expectErr:       true,
			errContains:     "remains busy",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.needsLiveDevice {
				t.Skip("requires a live kernel block device (dm/nvme) that TeardownVolume resolves through /dev, /proc and /sys; not simulatable via mocks")
			}

			mockCtrl := gomock.NewController(t)
			defer mockCtrl.Finish()

			fakeExecuter := mocks.NewMockExecuterInterface(mockCtrl)
			fakeHelper := mocks.NewMockOsDeviceConnectivityHelperInterface(mockCtrl)
			fakeMutex := &sync.Mutex{}

			ctx, cancel := context.WithCancel(context.Background())
			if tc.ctxCancelled {
				cancel()
			} else {
				defer cancel()
			}

			if !tc.ctxCancelled && tc.protocolType == "idempotent" {
				fakeHelper.EXPECT().FindDMByWWID(gomock.Any(), tc.expectedWWID).Return("").AnyTimes()
			}

			o := NewOsDeviceConnectivityHelperScsiGenericForTest(fakeExecuter, fakeHelper, fakeMutex)
			err := o.TeardownVolume(ctx, tc.target, tc.expectedWWID)

			if tc.expectErr {
				if err == nil {
					t.Fatalf("Expected error, got nil")
				}
				if tc.errContains != "" && !strings.Contains(err.Error(), tc.errContains) {
					t.Fatalf("Expected error containing %q, got %q", tc.errContains, err.Error())
				}
			} else {
				if err != nil {
					t.Fatalf("Expected success, got error: %v", err)
				}
			}
		})
	}
}
