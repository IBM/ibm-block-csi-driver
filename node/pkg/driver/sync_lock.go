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
	AddVolumeAndLunLock(id string, lun int, msg string) error
	RemoveVolumeAndLunLock(id string, lun int, msg string)
	GetSyncMap() *sync.Map
}

type SyncLock struct {
	SyncMap         *sync.Map
	Tokens          chan struct{}
	CleanScsiDevice bool
}

func NewSyncLock(max_invocations int, clean_scsi_device bool) SyncLockInterface {
	return &SyncLock{
		SyncMap:         &sync.Map{},
		Tokens:          make(chan struct{}, max_invocations),
		CleanScsiDevice: clean_scsi_device,
	}
}

func (s SyncLock) GetSyncMap() *sync.Map {
	return s.SyncMap
}

func (s SyncLock) AddVolumeAndLunLock(id string, lun int, msg string) error {
	err := s.addLunLock(lun, msg)
	if err != nil {
		return err
	}

	err = s.AddVolumeLock(id, msg)
	if err != nil {
		s.removeLunLock(lun, msg)
	}
	return err
}

func (s SyncLock) AddVolumeLock(id string, msg string) error {
	logger.Debugf("Lock for action %s, try to acquire lock for volume", msg)
	_, exists := s.SyncMap.LoadOrStore(id, 0)
	if exists {
		logger.Debugf("Lock for action %s, lock for volume %s is already in use by other thread", msg, id)
		return &VolumeAlreadyProcessingError{id}
	}

	select {
	case s.Tokens <- struct{}{}:
		logger.Debugf("Lock for action %s, succeeded to acquire lock for volume", msg)
		return nil
	case <-time.After(1 * time.Second):
		logger.Debugf("Lock for action %s, failed to acquire lock for volume", msg)
		s.SyncMap.Delete(id)
		return &VolumeNoResources{id}
	}
}

func (s SyncLock) RemoveVolumeAndLunLock(id string, lun int, msg string) {
	s.removeLunLock(lun, msg)
	s.RemoveVolumeLock(id, msg)
}

func (s SyncLock) RemoveVolumeLock(id string, msg string) {
	logger.Debugf("Lock for action %s, release lock for volume", msg)
	<-s.Tokens
	s.SyncMap.Delete(id)
}

func (s SyncLock) addLunLock(lun int, msg string) error {
	if !s.CleanScsiDevice {
		logger.Debugf("Clean devices disabled, skipping addLunLock") //Can be omitted, debug only.
		return nil
	}
	logger.Debugf("Lock for action %s, try to acquire lock for lun %d", msg, lun)

	_, exists := s.SyncMap.LoadOrStore(lun, 0)
	if exists {
		logger.Debugf("Lock for action %s, lock for lun %d is already in use by other thread", msg, lun)
		return &LunAlreadyProcessingError{lun}
	}
	return nil
}

func (s SyncLock) removeLunLock(lun int, msg string) {
	if !s.CleanScsiDevice {
		logger.Debugf("Clean devices disabled, skipping removeLunLock") //Can be omitted, debug only.
		return nil
	}
	logger.Debugf("Lock for action %s, release lock for lun %d", msg, lun)
	s.SyncMap.Delete(lun)
}
