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
	"k8s.io/klog"	
	"github.com/ibm/ibm-block-csi-driver/node/util"	
)

//go:generate mockgen -destination=../../mocks/mock_sync_lock.go -package=mocks github.com/ibm/ibm-block-csi-driver/node/pkg/driver SyncLockInterface

type SyncLockInterface interface {
	AddVolumeLock(id string, msg string) error
	RemoveVolumeLock(id string, msg string)
//    RemoveVolumeLock(id string, msg string) func()

}

type SyncLock struct {
	syncMap sync.Map
}

func NewSyncLock() *SyncLock {
	return &SyncLock{
		syncMap: sync.Map{},
	}

}

func (s SyncLock) AddVolumeLock(id string, msg string) error {
	goid := util.GetGoID()
	klog.V(5).Infof("Lock for action %s, Try to acquire lock for volume ID=%s (syncMap=%v) goid=%d", msg, id, s.syncMap, goid)
	result, exists := s.syncMap.Load(id)
	klog.V(5).Infof("Lock for action %s, Try to acquire lock for volume ID=%s  (result=%v, exists=%v) goid=%d", msg, id, result, exists, goid)
	if !exists {
		s.syncMap.Store(id, 0)
		klog.V(5).Infof("Lock for action %s, Succeed to acquire lock for volume ID=%s (syncMap=%v) goid=%d", msg, id, s.syncMap, goid)
		return nil
	} else {
		klog.V(5).Infof("Lock for action %s, Lock for volume ID=%s is already in use by other thread. goid=%d", msg, id, goid)		
		return &VolumeAlreadyProcessingError{id}
	}
}

func (s SyncLock) RemoveVolumeLock(id string, msg string) {
	goid := util.GetGoID()
	klog.V(5).Infof("Lock for action %s, release lock for volume ID=%s (syncMap=%v) goid=%d ", msg, id, s.syncMap, goid)

	_, exists := s.syncMap.Load(id)
	if exists {
		s.syncMap.Delete(id)
	}
}

/*func (s SyncLock) RemoveVolumeLock(id string, msg string) func() {
	return func() { s.RemoveVolumeLockDo(id, msg) }
}*/
