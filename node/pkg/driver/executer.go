package driver

import (
	"context"
	"os/exec"
	"time"
	"k8s.io/klog"
)

//go:generate mockgen -destination=../../mocks/mock_executer.go -package=mocks github.com/ibm/ibm-block-csi-driver/node/pkg/driver ExecutorInterface
type ExecutorInterface interface { // basic host dependent functions
	ExecuteWithTimeout(mSeconds int, command string, args []string) ([]byte, error)
}

type Executer struct {
	 nodeUtils NodeUtilsInterface
}

func (e *Executer) ExecuteWithTimeout(mSeconds int, command string, args []string) ([]byte, error) {
	klog.V(5).Infof("Executing command : {%v} with args : {%v}. and timeout : {%v} mseconds",command, args, mSeconds)
	
	// Create a new context and add a timeout to it
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(mSeconds)*time.Millisecond)
	defer cancel() // The cancel should be deferred so resources are cleaned up

	// Create the command with our context
	cmd := exec.CommandContext(ctx, command, args...)

	// This time we can simply use Output() to get the result.
	out, err := cmd.Output()

	// We want to check the context error to see if the timeout was executed.
	// The error returned by cmd.Output() will be OS specific based on what
	// happens when a process is killed.
	if ctx.Err() == context.DeadlineExceeded {
		klog.V(4).Infof("Command %s timeout reached", command)
		return nil, ctx.Err()
	}

	// If there's no context error, we know the command completed (or errored).
	klog.V(4).Infof("Output from command: %s", string(out))
	if err != nil {
		klog.V(4).Infof("Non-zero exit code: %s", err)
	}
	
	klog.V(5).Infof("Finished executing command",command, args)
	return out, err
}