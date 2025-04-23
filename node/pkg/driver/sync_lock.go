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
	"time"

	"github.com/ibm/ibm-block-csi-driver/node/logger"
)

//go:generate mockgen -destination=../../mocks/mock_sync_lock.go -package=mocks github.com/ibm/ibm-block-csi-driver/node/pkg/driver SyncLockInterface

type SyncLockInterface interface {
	AddVolumeLock(id string, msg string) error
	RemoveVolumeLock(id string, msg string)
	GetSyncMap() *sync.Map
	//    RemoveVolumeLock(id string, msg string) func()
}

type SyncLock struct {
	SyncMap *sync.Map
	Tokens  chan struct{}
}

func NewSyncLock() SyncLockInterface {
	return &SyncLock{
		SyncMap: &sync.Map{},
		Tokens:  make(chan struct{}, 2),
	}

}

func (s SyncLock) GetSyncMap() *sync.Map {
	return s.SyncMap
}

func (s SyncLock) AddVolumeLock(id string, msg string) error {
	logger.Debugf("Lock for action %s, Try to acquire lock for volume", msg)

        _, exists := s.SyncMap.LoadOrStore(id, 0)
        if exists {
                logger.Debugf("Lock for action %s, Lock for volume is already in use by other thread", msg)
                return &VolumeAlreadyProcessingError{id}
        }

	select {
        case s.Tokens <- struct{}{}:
		logger.Debugf("Lock for action %s, Succeed to acquire lock for volume", msg)
                return nil
	case <-time.After(1 * time.Second):
		logger.Debugf("Lock for action %s, failed to acquire execution semaphore", msg)
		s.SyncMap.Delete(id)
		return &VolumeAlreadyProcessingError{id}
	}
}

func (s SyncLock) RemoveVolumeLock(id string, msg string) {
	logger.Debugf("Lock for action %s, release lock for volume", msg)

	<-s.Tokens
	s.SyncMap.Delete(id)
}

/*func (s SyncLock) RemoveVolumeLock(id string, msg string) func() {
	return func() { s.RemoveVolumeLockDo(id, msg) }
}*/
