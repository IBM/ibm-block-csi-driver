// Copyright 2019 Hewlett Packard Enterprise Development LP

package sgio

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"os"
	"syscall"
	"unsafe"

	"github.com/ibm/ibm-block-csi-driver/node/logger"
)

const (
	sgGetVersionNumber = 0x2282
	sgIO               = 0x2285
	sgDxferFromDev     = -3
	senseBufLen        = 64
	timeout            = 20000
	respBufLen         = 96
	sgInfoOkMask       = 0x1
	sgInfoOk           = 0x0
)

var (
	// StandardInquiry :
	StandardInquiry = []uint8{
		0x12, // Operation Code
		0,    // EVPD
		0,    // VPD Page
		0,    // Reserved
		96,   // Response length
		0,    // Control
	}
	// Vpd80Inquiry :
	Vpd80Inquiry = []uint8{
		0x12, // Operation Code
		1,    // EVPD
		0x80, // VPD Page
		0,    // Reserved
		96,   // Response length
		0,    // Control
	}
)

// Hdr is our version of sg_io_hdr_t that gets passed to the sg_io ioctl
type Hdr struct {
	InterfaceID    int32
	DxferDirection int32
	CmdLen         uint8
	MxSbLen        uint8
	IovecCount     uint16
	DxferLen       uint32
	Dxferp         *byte
	Cmdp           *uint8
	Sbp            *byte
	Timeout        uint32
	Flags          uint32
	PackID         int32
	pad0           [4]byte
	UsrPtr         *byte
	Status         uint8
	MaskedStatus   uint8
	MsgStatus      uint8
	SbLenWr        uint8
	HostStatus     uint16
	DriverStatus   uint16
	Resid          int32
	Duration       uint32
	Info           uint32
}

func sgioSyscall(f *os.File, i *Hdr) error {
	return ioctl(f.Fd(), sgIO, uintptr(unsafe.Pointer(i)))
}

func ioctl(fd, cmd, ptr uintptr) error {
	_, _, err := syscall.Syscall(syscall.SYS_IOCTL, fd, cmd, ptr)
	if err != 0 {
		return err
	}
	return nil
}

func openScsiDevice(fname string) (*os.File, error) {
	f, err := os.OpenFile(fname, os.O_RDWR, 0)
	if err != nil {
		return nil, err
	}
	var version uint32
	if (ioctl(f.Fd(), sgGetVersionNumber, uintptr(unsafe.Pointer(&version))) != nil) || (version < 30000) {
		return nil, fmt.Errorf("device does not appear to be an sg device")
	}
	return f, nil
}

//ExecIoctl :
func ExecIoctl(inqCmdBlk []uint8, respBuf []byte, device string) error {
	f, err := openScsiDevice(device)
	defer f.Close()
	if err != nil {
		return err
	}
	senseBuf := make([]byte, senseBufLen)

	ioHdr := &Hdr{
		InterfaceID:    int32('S'),
		DxferDirection: sgDxferFromDev,
		Timeout:        timeout,
		CmdLen:         uint8(len(inqCmdBlk)),
		MxSbLen:        uint8(len(senseBuf)),
		DxferLen:       uint32(len(respBuf)),
		Dxferp:         &respBuf[0],
		Cmdp:           &inqCmdBlk[0],
		Sbp:            &senseBuf[0],
	}
	err = sgioSyscall(f, ioHdr)
	if err != nil {
		return err
	}
	return nil
}

//TestUnitReady to know if device is connected
func TestUnitReady(device string) error {
	logger.Info(">>>>>>>> TestUnitReady called for %s", device)
	defer logger.Info("<<<<<<< TestUnitReady")
	f, err := openScsiDevice(device)
	if err != nil {
		logger.Errorf("unable to open the device %s : %s", device, err.Error())
		return err
	}
	defer f.Close()
	senseBuf := make([]byte, senseBufLen)
	inqCmdBlk := []uint8{0, 0, 0, 0, 0, 0}
	ioHdr := &Hdr{
		InterfaceID:    int32('S'),
		CmdLen:         uint8(len(inqCmdBlk)),
		MxSbLen:        senseBufLen,
		DxferDirection: sgDxferFromDev,
		Cmdp:           &inqCmdBlk[0],
		Sbp:            &senseBuf[0],
		Timeout:        timeout,
	}
	err = sgioSyscall(f, ioHdr)
	if err != nil {
		logger.Errorf("unable to execute sgio call on device %s : %s", device, err.Error())
		return err
	}
	err = CheckSense(ioHdr, &senseBuf)
	if err != nil {
		logger.Errorf("unable to execute CheckSense on device %s : %s", device, err.Error())
		return err
	}
	return nil
}

// GetDeviceSerial returns unit serial number of the device using vpd page 0x80
func GetDeviceSerial(device string) (string, error) {
	logger.Info(">>> GetDeviceSerial called for %s", device)
	defer logger.Info("<<< GetDeviceSerial")
	respBuf := make([]byte, respBufLen)
	err := ExecIoctl(Vpd80Inquiry, respBuf, device)
	if err != nil {
		logger.Info("unable to obtain unit serial number on device %s, err %s", device, err.Error())
		return "", err
	}
	return string(respBuf[4:36]), nil
}

// CheckSense : checks the sense error code
func CheckSense(i *Hdr, s *[]byte) error {
	var b bytes.Buffer
	if (i.Info & sgInfoOkMask) != sgInfoOk {
		_, err := b.WriteString(
			fmt.Sprintf("SCSI response not ok\n"+
				"SCSI status: %v host status: %v driver status: %v",
				i.Status, i.HostStatus, i.DriverStatus))
		if err != nil {
			return err
		}
		if i.SbLenWr > 0 {
			_, err := b.WriteString(
				fmt.Sprintf("\nSENSE (raw):\n%x\nASC: %d, ASCQ: %d", *s, (*s)[12], (*s)[13]))
			if err != nil {
				return err
			}
		}
		return fmt.Errorf(b.String())
	}
	return nil
}
func stringify(a, b byte) string {
	return dumpHex(append([]byte{a}, b))
}
func dumpHex(data []byte) string {
	var buf bytes.Buffer
	var tmp [3]byte
	for i := range data {
		hex.Encode(tmp[:], data[i:i+1])
		tmp[2] = ' '
		_, err := buf.Write(tmp[:3])
		if err != nil {
			return ""
		}
	}
	return buf.String()
}
