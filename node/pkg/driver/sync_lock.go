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
	"sync"
)

//go:generate mockgen -destination=../../mocks/mock_sync_lock.go -package=mocks github.com/ibm/ibm-block-csi-driver/node/pkg/driver SyncLockInterface

type SyncLockInterface interface {
	AddVolumeLock(id string) error
	RemoveVolumeLock(id string)
}

type SyncLock struct {
	syncMap sync.Map
}

func NewSyncLock() *SyncLock {
	return &SyncLock{
		syncMap: sync.Map{},
	}

}

func (s SyncLock) AddVolumeLock(id string) error {
	_, exists := s.syncMap.Load(id)
	if !exists {
		s.syncMap.Store(id, 0)
		return nil
	} else {
		return &VolumeAlreadyProcessingError{id}
	}
	return nil
}

func (s SyncLock) RemoveVolumeLock(id string) {
	_, exists := s.syncMap.Load(id)
	if exists {
		s.syncMap.Delete(id)
	}
}
