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

package device_connectivity

import (
	"context"
)

//go:generate mockgen -destination=../../../mocks/mock_OsDeviceConnectivityInterface.go -package=mocks github.com/ibm/ibm-block-csi-driver/node/pkg/driver/device_connectivity OsDeviceConnectivityInterface

type OsDeviceConnectivityInterface interface {
	EnsureLogin(ctx context.Context, ipsByArrayIdentifier map[string][]string)    // For iSCSI login
	RescanDevices(lunId int, arrayIdentifier []string) error // For NVME lunID will be namespace ID.
	GetMpathDevice(ctx context.Context, volumeId string) (string, error)
	RemovePhysicalDevice(ctx context.Context, sysDevices []string) error
	RemoveGhostDevice(ctx context.Context, expectedSerial string, expectedLun int, arrayIdentifiers []string) error
	ValidateLun(ctx context.Context, targetDm string, lun int, sysDevices []string, expectedSerial string) error
}
