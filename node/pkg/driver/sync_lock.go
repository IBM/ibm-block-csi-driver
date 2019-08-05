package driver

import (
	"sync"
)

//go:generate mockgen -destination=../../mocks/mock_sync_lock.go -package=mocks github.com/ibm/ibm-block-csi-driver/node/pkg/driver SyncLockInterface

type SyncLockInterface interface {
	AddVolumeLock(id string) error
	RemoveVolumeLock( id string)
}

type SyncLock struct {
	syncMap sync.Map
}

func NewSyncLock() *SyncLock {
	return &SyncLock{
		syncMap: sync.Map{},
	}

}

func (s SyncLock) AddVolumeLock( id string) error {
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
