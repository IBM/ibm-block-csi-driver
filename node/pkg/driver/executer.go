package driver

import (
	"k8s.io/klog"
	"os/exec"
	"time"
	"fmt"
)

//go:generate mockgen -destination=../../mocks/mock_executer.go -package=mocks github.com/ibm/ibm-block-csi-driver/node/pkg/driver ExecutorInterface
type ExecutorInterface interface { // basic host dependent functions
	ExecuteWithTimeout(mSeconds int, command string, args []string) ([]byte, error)
}

type Executer struct {
	nodeUtils NodeUtilsInterface
}

// execCommandResult is used to return shell command results via channels between goroutines
type execCommandResult struct {
	Output []byte
	Error  error
}

func (e *Executer) ExecuteWithTimeout(mSeconds int, command string, args []string) ([]byte, error) {
	klog.V(4).Infof("executing with timeout")
	timeout :=  time.Duration(mSeconds)*time.Millisecond
	klog.V(4).Infof("aftertimeout1")

	cmd := exec.Command(command, args...)
	klog.V(4).Infof("aftercmd")
	done := make(chan execCommandResult, 1)
	klog.V(4).Infof("done")
	var result execCommandResult
	klog.V(4).Infof("cmd : {%v}", cmd)

	go func() {
		klog.V(4).Infof("go func")
		out, err := cmd.CombinedOutput()
		klog.V(4).Infof("out {%v} err {%v}", out, err)
		done <- execCommandResult{Output: out, Error: err}
		klog.V(4).Infof("done?")
	}()

	select {
	case <-time.After(timeout):
		klog.V(4).Infof("afte rtimeout?")
		if err := cmd.Process.Kill(); err != nil {
			klog.Errorf("Failed to kill process. command : {%v}, err : {%v}", command, err.Error())
			result = execCommandResult{Output: nil, Error: err}
		} else {
			klog.Errorf("command :{%v} killed after timeout exceeded.", command)
			result = execCommandResult{Output: nil, Error: fmt.Errorf("command timeout exceeded")}
		}
	case result = <-done:
		klog.V(4).Infof("done aftere rtimeout?")
		break
	}

	klog.V(4).Infof("command : %v completed. output : {%v} error : {%v}", command, string(result.Output), result.Error)
	return result.Output, result.Error
}
