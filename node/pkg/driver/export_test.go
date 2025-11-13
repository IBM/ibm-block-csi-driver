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

package driver

import (
	"context"

	"github.com/ibm/ibm-block-csi-driver/node/pkg/driver/device_connectivity"
	"github.com/ibm/ibm-block-csi-driver/node/pkg/driver/executer"
)

// SetDevicesAreNvmeForTest replaces the sysfs-reading devicesAreNvme with a fake.
// It returns a function that restores the original.
func SetDevicesAreNvmeForTest(f func(ctx context.Context, gater *executer.KeyedGater, device string) (device_connectivity.NvmeType, error)) func() {
	orig := devicesAreNvme
	devicesAreNvme = f
	return func() { devicesAreNvme = orig }
}

// SetGetSysDevicesFromMpathForTest replaces the sysfs-reading getSysDevicesFromMpath with a fake.
// It returns a function that restores the original.
func SetGetSysDevicesFromMpathForTest(f func(ctx context.Context, gater *executer.KeyedGater, baseDevice string) ([]string, error)) func() {
	orig := getSysDevicesFromMpath
	getSysDevicesFromMpath = f
	return func() { getSysDevicesFromMpath = orig }
}
