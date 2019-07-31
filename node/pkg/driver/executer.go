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

	timeout :=  time.Duration(mSeconds)*time.Millisecond

	cmd := exec.Command(command, args...)
	done := make(chan execCommandResult, 1)
	var result execCommandResult

	go func() {
		out, err := cmd.CombinedOutput()
		done <- execCommandResult{Output: out, Error: err}
	}()

	select {
	case <-time.After(timeout):
		if err := cmd.Process.Kill(); err != nil {
			klog.Errorf("Failed to kill process. command : {%v}, err : {%v}", command, err.Error())
			result = execCommandResult{Output: nil, Error: err}
		} else {
			klog.Errorf("command :{%v} killed after timeout exceeded.", command)
			result = execCommandResult{Output: nil, Error: fmt.Errorf("command timeout exceeded")}
		}
	case result = <-done:
		break
	}

	klog.V(4).Infof("command : %v completed. output : {%v} error : {%v}", command, string(result.Output), result.Error)
	return result.Output, result.Error
}
