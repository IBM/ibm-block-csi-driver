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

// We can map goid to a string which will appear in log entry with this goid. In most places it will be volume id.
// Use "SetAdditionalIDInfo(<info>)" at the beginning of API method to specify additional info for current goid.
// Directly after use "defer DeleteAdditionalIDInfo()" to remove the info so additionalIDInfoByGoID will not grow endlessly.

package goid_info

import (
	"github.com/ibm/ibm-block-csi-driver/node/util"
	"golang.org/x/sync/syncmap"
)

var additionalIDInfoByGoID = new(syncmap.Map)

func GetAdditionalIDInfo() (string, bool) {
	goId := util.GetGoID()
	additionalIDInfo, hasValue := additionalIDInfoByGoID.Load(goId)
	if hasValue {
		return additionalIDInfo.(string), hasValue
	} else {
		return "", hasValue
	}
}

func SetAdditionalIDInfo(additionalIDInfo string) {
	goId := util.GetGoID()
	additionalIDInfoByGoID.Store(goId, additionalIDInfo)
}

func DeleteAdditionalIDInfo() {
	goId := util.GetGoID()
	additionalIDInfoByGoID.Delete(goId)
}
