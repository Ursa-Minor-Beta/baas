package exec

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"os/signal"
	"syscall"

	"github.com/pkg/errors"
)

// Exec defines execution on host environment
type Exec struct {
	context context.Context
	output  *bytes.Buffer
}

// ExecRes result of execution
type ExecRes struct {
	Pid int
	Env []string
}

// Opts execution options
type Opts struct {
	Wd       string
	Env      []string
	Callback func(string)
}

// NewExec initializes new host executor
func NewExec(context context.Context) Exec {
	return Exec{context: context}
}

// NewExecWithOutput initializes new host executor
func NewExecWithOutput(context context.Context, output *bytes.Buffer) Exec {
	res := NewExec(context)
	res.output = output
	return res
}

// Lookup returns path to command
func Lookup(command string) (string, error) {
	return exec.LookPath(command)
}

// ExecCommand executes command and returns output
func (e *Exec) ExecCommand(cmd []string, opts Opts) (string, error) {
	run := e.prepareCommand(cmd, opts)
	res, err := run.CombinedOutput()
	return string(res), err
}

// ProxyExec executes command with all binding to parent process
func (e *Exec) ProxyExec(cmd []string, opts Opts) error {
	run := e.prepareCommand(cmd, opts)
	run.Stdout = os.Stdout
	run.Stdin = os.Stdin
	run.Stderr = os.Stderr
	if err := run.Start(); err != nil {
		return errors.Wrapf(err, "failed to start command '%s'", cmd)
	}
	// wait for the command to finish
	waitCh := make(chan error, 1)
	go func() {
		waitCh <- run.Wait()
		close(waitCh)
	}()
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan)
	// You need a for loop to handle multiple signals
	for {
		select {
		case sig := <-sigChan:
			if run.ProcessState == nil || run.ProcessState.Exited() {
				return nil
			}
			if err := run.Process.Signal(sig); err != nil {
				return errors.Wrapf(err, "error sending signal %s", sig)
			}
		case err := <-waitCh:
			// Subprocess exited. Get the return code, if we can
			var waitStatus syscall.WaitStatus
			if exitError, ok := err.(*exec.ExitError); ok {
				waitStatus = exitError.Sys().(syscall.WaitStatus)
				os.Exit(waitStatus.ExitStatus())
			}
			return err
		}
	}
}

func (e *Exec) prepareCommand(cmd []string, opts Opts) *exec.Cmd {
	run := exec.CommandContext(e.context, cmd[0], cmd[1:]...)
	if len(opts.Env) > 0 {
		run.Env = os.Environ()
		run.Env = append(run.Env, opts.Env...)
	}
	if opts.Wd != "" {
		run.Dir = opts.Wd
	}
	return run
}

func CommandExists(cmd string) bool {
	_, err := exec.LookPath(cmd)
	return err == nil
}
