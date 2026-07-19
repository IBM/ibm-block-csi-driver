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
	"os"
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/ibm/ibm-block-csi-driver/node/mocks"
	"github.com/ibm/ibm-block-csi-driver/node/pkg/driver/device_connectivity"
)

// Native NVMe discovery test fixtures. The volume UID → NGUID derivation is
// convertScsiIdToNguid; for this SCSI UID the kernel wwid / by-id name is the
// undashed 32-hex NGUID below (confirmed against a live host's /sys/block/*/wwid).
const (
	nativeVolumeUID   = "60050768108187245800000000000039"
	nativeNguid       = "58000000000000390050760810818724"
	nativeCoreParam   = "/sys/module/nvme_core/parameters/multipath"
	nativeByIdPath    = "/dev/disk/by-id/nvme-eui." + nativeNguid
	nativeWwidGlob    = "/sys/block/nvme*/wwid"
	nativeHeadDevice  = "/dev/nvme1n1"
	otherWwidFilePath = "/sys/block/nvme0n1/wwid"
	headWwidFilePath  = "/sys/block/nvme1n1/wwid"
)

func newNativeNvmeOFc(
	exec *mocks.MockExecuterInterface,
	helper *mocks.MockOsDeviceConnectivityHelperScsiGenericInterface,
) device_connectivity.OsDeviceConnectivityInterface {
	return &device_connectivity.OsDeviceConnectivityNvmeOFc{Executer: exec, HelperScsiGeneric: helper}
}

func expectNativeMode(exec *mocks.MockExecuterInterface, on bool) {
	val := "N"
	if on {
		val = "Y"
	}
	exec.EXPECT().IoutilReadFile(nativeCoreParam).Return([]byte(val), nil).AnyTimes()
}

func TestNvmeOFcGetMpathDevice_Native(t *testing.T) {
	t.Run("by-id symlink resolves the namespace head", func(t *testing.T) {
		mockCtrl := gomock.NewController(t)
		defer mockCtrl.Finish()
		exec := mocks.NewMockExecuterInterface(mockCtrl)
		expectNativeMode(exec, true)
		exec.EXPECT().OsReadlink(nativeByIdPath).Return("../../nvme1n1", nil)

		got, err := newNativeNvmeOFc(exec, nil).GetMpathDevice(nativeVolumeUID)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != nativeHeadDevice {
			t.Fatalf("expected %s, got %s", nativeHeadDevice, got)
		}
	})

	t.Run("sysfs wwid fallback when by-id symlink absent", func(t *testing.T) {
		mockCtrl := gomock.NewController(t)
		defer mockCtrl.Finish()
		exec := mocks.NewMockExecuterInterface(mockCtrl)
		expectNativeMode(exec, true)
		exec.EXPECT().OsReadlink(nativeByIdPath).Return("", os.ErrNotExist).AnyTimes()
		exec.EXPECT().FilepathGlob(nativeWwidGlob).
			Return([]string{otherWwidFilePath, headWwidFilePath}, nil).AnyTimes()
		exec.EXPECT().IoutilReadFile(otherWwidFilePath).
			Return([]byte("eui.aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"), nil).AnyTimes()
		// Dashed sysfs form must still match the undashed derived NGUID (normalization).
		exec.EXPECT().IoutilReadFile(headWwidFilePath).
			Return([]byte("eui.58000000-0000-0039-0050-760810818724\n"), nil).AnyTimes()

		got, err := newNativeNvmeOFc(exec, nil).GetMpathDevice(nativeVolumeUID)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != nativeHeadDevice {
			t.Fatalf("expected %s, got %s", nativeHeadDevice, got)
		}
	})

	t.Run("namespace not present -> MultipathDeviceNotFoundForVolumeError", func(t *testing.T) {
		mockCtrl := gomock.NewController(t)
		defer mockCtrl.Finish()
		exec := mocks.NewMockExecuterInterface(mockCtrl)
		expectNativeMode(exec, true)
		exec.EXPECT().OsReadlink(nativeByIdPath).Return("", os.ErrNotExist).AnyTimes()
		exec.EXPECT().FilepathGlob(nativeWwidGlob).Return([]string{}, nil).AnyTimes()

		_, err := newNativeNvmeOFc(exec, nil).GetMpathDevice(nativeVolumeUID)
		if err == nil {
			t.Fatalf("expected not-found error, got nil")
		}
		if _, ok := err.(*device_connectivity.MultipathDeviceNotFoundForVolumeError); !ok {
			t.Fatalf("expected MultipathDeviceNotFoundForVolumeError, got %T: %v", err, err)
		}
	})
}

func TestNvmeOFcGetMpathDevice_NonNativeDelegates(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()
	exec := mocks.NewMockExecuterInterface(mockCtrl)
	helper := mocks.NewMockOsDeviceConnectivityHelperScsiGenericInterface(mockCtrl)
	expectNativeMode(exec, false)
	// dm-multipath mode: GetMpathDevice must delegate to the shared helper unchanged.
	helper.EXPECT().GetMpathDevice(nativeVolumeUID).Return("/dev/dm-3", nil)

	got, err := newNativeNvmeOFc(exec, helper).GetMpathDevice(nativeVolumeUID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "/dev/dm-3" {
		t.Fatalf("expected /dev/dm-3 (delegated), got %s", got)
	}
}

func TestIsVolumePathMatchesVolumeId_Native(t *testing.T) {
	const volumePath = "/var/lib/kubelet/pods/x/volumes/kubernetes.io~csi/pvc/mount"
	variations := []string{nativeVolumeUID, nativeNguid}

	t.Run("native head wwid matches the volume", func(t *testing.T) {
		mockCtrl := gomock.NewController(t)
		defer mockCtrl.Finish()
		exec := mocks.NewMockExecuterInterface(mockCtrl)
		helper := mocks.NewMockOsDeviceConnectivityHelperInterface(mockCtrl)
		o := NewOsDeviceConnectivityHelperScsiGenericForTest(exec, helper, nil)

		helper.EXPECT().GetVolumeIdVariations(nativeVolumeUID).Return(variations)
		helper.EXPECT().GetMpathDeviceName(volumePath).Return("nvme1n1", nil)
		exec.EXPECT().IoutilReadFile(nativeCoreParam).Return([]byte("Y"), nil)
		exec.EXPECT().IoutilReadFile("/sys/block/nvme1n1/wwid").Return([]byte("eui."+nativeNguid+"\n"), nil)

		match, err := o.IsVolumePathMatchesVolumeId(nativeVolumeUID, volumePath)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !match {
			t.Fatalf("expected native wwid to match volume")
		}
	})

	t.Run("native head wwid belongs to a different volume", func(t *testing.T) {
		mockCtrl := gomock.NewController(t)
		defer mockCtrl.Finish()
		exec := mocks.NewMockExecuterInterface(mockCtrl)
		helper := mocks.NewMockOsDeviceConnectivityHelperInterface(mockCtrl)
		o := NewOsDeviceConnectivityHelperScsiGenericForTest(exec, helper, nil)

		helper.EXPECT().GetVolumeIdVariations(nativeVolumeUID).Return(variations)
		helper.EXPECT().GetMpathDeviceName(volumePath).Return("nvme2n1", nil)
		exec.EXPECT().IoutilReadFile(nativeCoreParam).Return([]byte("Y"), nil)
		exec.EXPECT().IoutilReadFile("/sys/block/nvme2n1/wwid").
			Return([]byte("eui.99990000000000390050760810818724"), nil)

		match, err := o.IsVolumePathMatchesVolumeId(nativeVolumeUID, volumePath)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if match {
			t.Fatalf("expected mismatch for a different volume's wwid")
		}
	})
}
