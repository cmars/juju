// Copyright 2014-2015 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package operation_test

import (
	"time"

	"github.com/juju/errors"
	"github.com/juju/testing"
	jc "github.com/juju/testing/checkers"
	gc "gopkg.in/check.v1"
	"gopkg.in/juju/charm.v4/hooks"

	"github.com/juju/juju/worker/uniter/hook"
	"github.com/juju/juju/worker/uniter/operation"
	"github.com/juju/juju/worker/uniter/runner"
)

type RunHookSuite struct {
	testing.IsolationSuite
}

var _ = gc.Suite(&RunHookSuite{})

type newHook func(operation.Factory, hook.Info) (operation.Operation, error)

func (s *RunHookSuite) testClearResolvedFlagError(c *gc.C, newHook newHook) {
	callbacks := &PrepareHookCallbacks{
		MockClearResolvedFlag: &MockNoArgs{err: errors.New("biff")},
	}
	factory := operation.NewFactory(nil, nil, callbacks, nil, nil)
	op, err := newHook(factory, hook.Info{Kind: hooks.ConfigChanged})
	c.Assert(err, jc.ErrorIsNil)

	newState, err := op.Prepare(operation.State{})
	c.Check(newState, gc.IsNil)
	c.Check(callbacks.MockClearResolvedFlag.called, jc.IsTrue)
	c.Check(err, gc.ErrorMatches, "biff")
}

func (s *RunHookSuite) TestClearResolvedFlagError_Retry(c *gc.C) {
	s.testClearResolvedFlagError(c, (operation.Factory).NewRetryHook)
}

func (s *RunHookSuite) TestClearResolvedFlagError_Skip(c *gc.C) {
	s.testClearResolvedFlagError(c, (operation.Factory).NewSkipHook)
}

func (s *RunHookSuite) testPrepareHookError(
	c *gc.C, newHook newHook, expectClearResolvedFlag, expectSkip bool,
) {
	callbacks := &PrepareHookCallbacks{
		MockPrepareHook:       &MockPrepareHook{err: errors.New("pow")},
		MockClearResolvedFlag: &MockNoArgs{},
	}
	factory := operation.NewFactory(nil, nil, callbacks, nil, nil)
	op, err := newHook(factory, hook.Info{Kind: hooks.ConfigChanged})
	c.Assert(err, jc.ErrorIsNil)

	newState, err := op.Prepare(operation.State{})
	c.Check(newState, gc.IsNil)
	c.Check(callbacks.MockClearResolvedFlag.called, gc.Equals, expectClearResolvedFlag)
	if expectSkip {
		c.Check(err, gc.Equals, operation.ErrSkipExecute)
		c.Check(callbacks.MockPrepareHook.gotHook, gc.IsNil)
		return
	}
	c.Check(err, gc.ErrorMatches, "pow")
	c.Check(callbacks.MockPrepareHook.gotHook, gc.DeepEquals, &hook.Info{
		Kind: hooks.ConfigChanged,
	})
}

func (s *RunHookSuite) TestPrepareHookError_Run(c *gc.C) {
	s.testPrepareHookError(c, (operation.Factory).NewRunHook, false, false)
}

func (s *RunHookSuite) TestPrepareHookError_Retry(c *gc.C) {
	s.testPrepareHookError(c, (operation.Factory).NewRetryHook, true, false)
}

func (s *RunHookSuite) TestPrepareHookError_Skip(c *gc.C) {
	s.testPrepareHookError(c, (operation.Factory).NewSkipHook, true, true)
}

func (s *RunHookSuite) testPrepareRunnerError(c *gc.C, newHook newHook) {
	callbacks := NewPrepareHookCallbacks()
	runnerFactory := &MockRunnerFactory{
		MockNewHookRunner: &MockNewHookRunner{err: errors.New("splat")},
	}
	factory := operation.NewFactory(nil, runnerFactory, callbacks, nil, nil)
	op, err := newHook(factory, hook.Info{Kind: hooks.ConfigChanged})
	c.Assert(err, jc.ErrorIsNil)

	newState, err := op.Prepare(operation.State{})
	c.Check(newState, gc.IsNil)
	c.Check(err, gc.ErrorMatches, "splat")
	c.Check(runnerFactory.MockNewHookRunner.gotHook, gc.DeepEquals, &hook.Info{
		Kind: hooks.ConfigChanged,
	})
}

func (s *RunHookSuite) TestPrepareRunnerError_Run(c *gc.C) {
	s.testPrepareRunnerError(c, (operation.Factory).NewRunHook)
}

func (s *RunHookSuite) TestPrepareRunnerError_Retry(c *gc.C) {
	s.testPrepareRunnerError(c, (operation.Factory).NewRetryHook)
}

func (s *RunHookSuite) testPrepareSuccess(
	c *gc.C, newHook newHook, before, after operation.State,
) {
	runnerFactory := NewRunHookRunnerFactory(errors.New("should not call"))
	callbacks := NewPrepareHookCallbacks()
	factory := operation.NewFactory(nil, runnerFactory, callbacks, nil, nil)
	op, err := newHook(factory, hook.Info{Kind: hooks.ConfigChanged})
	c.Assert(err, jc.ErrorIsNil)

	newState, err := op.Prepare(before)
	c.Check(err, jc.ErrorIsNil)
	c.Check(newState, gc.DeepEquals, &after)
}

func (s *RunHookSuite) TestPrepareSuccess_BlankSlate(c *gc.C) {
	for i, newHook := range []newHook{
		(operation.Factory).NewRunHook,
		(operation.Factory).NewRetryHook,
	} {
		c.Logf("variant %d", i)
		s.testPrepareSuccess(c,
			newHook,
			operation.State{},
			operation.State{
				Kind: operation.RunHook,
				Step: operation.Pending,
				Hook: &hook.Info{Kind: hooks.ConfigChanged},
			},
		)
	}
}

func (s *RunHookSuite) TestPrepareSuccess_Preserve(c *gc.C) {
	for i, newHook := range []newHook{
		(operation.Factory).NewRunHook,
		(operation.Factory).NewRetryHook,
	} {
		c.Logf("variant %d", i)
		s.testPrepareSuccess(c,
			newHook,
			overwriteState,
			operation.State{
				Started:            true,
				CollectMetricsTime: 1234567,
				Kind:               operation.RunHook,
				Step:               operation.Pending,
				Hook:               &hook.Info{Kind: hooks.ConfigChanged},
			},
		)
	}
}

func (s *RunHookSuite) testExecuteLockError(c *gc.C, newHook newHook) {
	runnerFactory := NewRunHookRunnerFactory(errors.New("should not call"))
	callbacks := &ExecuteHookCallbacks{
		PrepareHookCallbacks:     NewPrepareHookCallbacks(),
		MockAcquireExecutionLock: &MockAcquireExecutionLock{err: errors.New("blart")},
	}
	factory := operation.NewFactory(nil, runnerFactory, callbacks, nil, nil)
	op, err := newHook(factory, hook.Info{Kind: hooks.ConfigChanged})
	c.Assert(err, jc.ErrorIsNil)
	_, err = op.Prepare(operation.State{})
	c.Assert(err, jc.ErrorIsNil)

	newState, err := op.Execute(operation.State{})
	c.Assert(newState, gc.IsNil)
	c.Assert(err, gc.ErrorMatches, "blart")
	c.Assert(*callbacks.MockAcquireExecutionLock.gotMessage, gc.Equals, "running hook some-hook-name")
}

func (s *RunHookSuite) TestExecuteLockError_Run(c *gc.C) {
	s.testExecuteLockError(c, (operation.Factory).NewRunHook)
}

func (s *RunHookSuite) TestExecuteLockError_Retry(c *gc.C) {
	s.testExecuteLockError(c, (operation.Factory).NewRetryHook)
}

func (s *RunHookSuite) getExecuteRunnerTest(c *gc.C, newHook newHook, runErr error) (operation.Operation, *ExecuteHookCallbacks, *MockRunnerFactory) {
	runnerFactory := NewRunHookRunnerFactory(runErr)
	callbacks := &ExecuteHookCallbacks{
		PrepareHookCallbacks:     NewPrepareHookCallbacks(),
		MockAcquireExecutionLock: &MockAcquireExecutionLock{},
		MockNotifyHookCompleted:  &MockNotify{},
		MockNotifyHookFailed:     &MockNotify{},
	}
	factory := operation.NewFactory(nil, runnerFactory, callbacks, nil, nil)
	op, err := newHook(factory, hook.Info{Kind: hooks.ConfigChanged})
	c.Assert(err, jc.ErrorIsNil)
	return op, callbacks, runnerFactory
}

func (s *RunHookSuite) testExecuteMissingHookError(c *gc.C, newHook newHook) {
	runErr := runner.NewMissingHookError("blah-blah")
	op, callbacks, runnerFactory := s.getExecuteRunnerTest(c, newHook, runErr)
	_, err := op.Prepare(operation.State{})
	c.Assert(err, jc.ErrorIsNil)

	newState, err := op.Execute(operation.State{})
	c.Assert(err, jc.ErrorIsNil)
	c.Assert(newState, gc.DeepEquals, &operation.State{
		Kind: operation.RunHook,
		Step: operation.Done,
		Hook: &hook.Info{Kind: hooks.ConfigChanged},
	})
	c.Assert(*callbacks.MockAcquireExecutionLock.gotMessage, gc.Equals, "running hook some-hook-name")
	c.Assert(callbacks.MockAcquireExecutionLock.didUnlock, jc.IsTrue)
	c.Assert(*runnerFactory.MockNewHookRunner.runner.MockRunHook.gotName, gc.Equals, "some-hook-name")
	c.Assert(callbacks.MockNotifyHookCompleted.gotName, gc.IsNil)
	c.Assert(callbacks.MockNotifyHookFailed.gotName, gc.IsNil)
}

func (s *RunHookSuite) TestExecuteMissingHookError_Run(c *gc.C) {
	s.testExecuteMissingHookError(c, (operation.Factory).NewRunHook)
}

func (s *RunHookSuite) TestExecuteMissingHookError_Retry(c *gc.C) {
	s.testExecuteMissingHookError(c, (operation.Factory).NewRetryHook)
}

func (s *RunHookSuite) testExecuteRequeueRebootError(c *gc.C, newHook newHook) {
	runErr := runner.ErrRequeueAndReboot
	op, callbacks, runnerFactory := s.getExecuteRunnerTest(c, newHook, runErr)
	_, err := op.Prepare(operation.State{})
	c.Assert(err, jc.ErrorIsNil)

	newState, err := op.Execute(operation.State{})
	c.Assert(err, gc.Equals, operation.ErrNeedsReboot)
	c.Assert(newState, gc.DeepEquals, &operation.State{
		Kind: operation.RunHook,
		Step: operation.Queued,
		Hook: &hook.Info{Kind: hooks.ConfigChanged},
	})
	c.Assert(*callbacks.MockAcquireExecutionLock.gotMessage, gc.Equals, "running hook some-hook-name")
	c.Assert(callbacks.MockAcquireExecutionLock.didUnlock, jc.IsTrue)
	c.Assert(*runnerFactory.MockNewHookRunner.runner.MockRunHook.gotName, gc.Equals, "some-hook-name")
	c.Assert(*callbacks.MockNotifyHookCompleted.gotName, gc.Equals, "some-hook-name")
	c.Assert(*callbacks.MockNotifyHookCompleted.gotContext, gc.Equals, runnerFactory.MockNewHookRunner.runner.context)
	c.Assert(callbacks.MockNotifyHookFailed.gotName, gc.IsNil)
}

func (s *RunHookSuite) TestExecuteRequeueRebootError_Run(c *gc.C) {
	s.testExecuteRequeueRebootError(c, (operation.Factory).NewRunHook)
}

func (s *RunHookSuite) TestExecuteRequeueRebootError_Retry(c *gc.C) {
	s.testExecuteRequeueRebootError(c, (operation.Factory).NewRetryHook)
}

func (s *RunHookSuite) testExecuteRebootError(c *gc.C, newHook newHook) {
	runErr := runner.ErrReboot
	op, callbacks, runnerFactory := s.getExecuteRunnerTest(c, newHook, runErr)
	_, err := op.Prepare(operation.State{})
	c.Assert(err, jc.ErrorIsNil)

	newState, err := op.Execute(operation.State{})
	c.Assert(err, gc.Equals, operation.ErrNeedsReboot)
	c.Assert(newState, gc.DeepEquals, &operation.State{
		Kind: operation.RunHook,
		Step: operation.Done,
		Hook: &hook.Info{Kind: hooks.ConfigChanged},
	})
	c.Assert(*callbacks.MockAcquireExecutionLock.gotMessage, gc.Equals, "running hook some-hook-name")
	c.Assert(callbacks.MockAcquireExecutionLock.didUnlock, jc.IsTrue)
	c.Assert(*runnerFactory.MockNewHookRunner.runner.MockRunHook.gotName, gc.Equals, "some-hook-name")
	c.Assert(*callbacks.MockNotifyHookCompleted.gotName, gc.Equals, "some-hook-name")
	c.Assert(*callbacks.MockNotifyHookCompleted.gotContext, gc.Equals, runnerFactory.MockNewHookRunner.runner.context)
	c.Assert(callbacks.MockNotifyHookFailed.gotName, gc.IsNil)
}

func (s *RunHookSuite) TestExecuteRebootError_Run(c *gc.C) {
	s.testExecuteRebootError(c, (operation.Factory).NewRunHook)
}

func (s *RunHookSuite) TestExecuteRebootError_Retry(c *gc.C) {
	s.testExecuteRebootError(c, (operation.Factory).NewRetryHook)
}

func (s *RunHookSuite) testExecuteOtherError(c *gc.C, newHook newHook) {
	runErr := errors.New("graaargh")
	op, callbacks, runnerFactory := s.getExecuteRunnerTest(c, newHook, runErr)
	_, err := op.Prepare(operation.State{})
	c.Assert(err, jc.ErrorIsNil)

	newState, err := op.Execute(operation.State{})
	c.Assert(err, gc.Equals, operation.ErrHookFailed)
	c.Assert(newState, gc.IsNil)
	c.Assert(*callbacks.MockAcquireExecutionLock.gotMessage, gc.Equals, "running hook some-hook-name")
	c.Assert(callbacks.MockAcquireExecutionLock.didUnlock, jc.IsTrue)
	c.Assert(*runnerFactory.MockNewHookRunner.runner.MockRunHook.gotName, gc.Equals, "some-hook-name")
	c.Assert(*callbacks.MockNotifyHookFailed.gotName, gc.Equals, "some-hook-name")
	c.Assert(*callbacks.MockNotifyHookFailed.gotContext, gc.Equals, runnerFactory.MockNewHookRunner.runner.context)
	c.Assert(callbacks.MockNotifyHookCompleted.gotName, gc.IsNil)
}

func (s *RunHookSuite) TestExecuteOtherError_Run(c *gc.C) {
	s.testExecuteOtherError(c, (operation.Factory).NewRunHook)
}

func (s *RunHookSuite) TestExecuteOtherError_Retry(c *gc.C) {
	s.testExecuteOtherError(c, (operation.Factory).NewRetryHook)
}

func (s *RunHookSuite) testExecuteSuccess(
	c *gc.C, newHook newHook, before, after operation.State,
) {
	op, _, _ := s.getExecuteRunnerTest(c, newHook, nil)
	midState, err := op.Prepare(before)
	c.Assert(err, jc.ErrorIsNil)
	c.Assert(midState, gc.NotNil)

	newState, err := op.Execute(*midState)
	c.Assert(err, jc.ErrorIsNil)
	c.Assert(newState, gc.DeepEquals, &after)
}

func (s *RunHookSuite) TestExecuteSuccess_BlankSlate(c *gc.C) {
	for i, newHook := range []newHook{
		(operation.Factory).NewRunHook,
		(operation.Factory).NewRetryHook,
	} {
		c.Logf("variant %d", i)
		s.testExecuteSuccess(c,
			newHook,
			operation.State{},
			operation.State{
				Kind: operation.RunHook,
				Step: operation.Done,
				Hook: &hook.Info{Kind: hooks.ConfigChanged},
			},
		)
	}
}

func (s *RunHookSuite) TestExecuteSuccess_Preserve(c *gc.C) {
	for i, newHook := range []newHook{
		(operation.Factory).NewRunHook,
		(operation.Factory).NewRetryHook,
	} {
		c.Logf("variant %d", i)
		s.testExecuteSuccess(c,
			newHook,
			overwriteState,
			operation.State{
				Started:            true,
				CollectMetricsTime: 1234567,
				Kind:               operation.RunHook,
				Step:               operation.Done,
				Hook:               &hook.Info{Kind: hooks.ConfigChanged},
			},
		)
	}
}

func (s *RunHookSuite) testCommitError(c *gc.C, newHook newHook) {
	callbacks := &CommitHookCallbacks{
		MockCommitHook: &MockCommitHook{nil, errors.New("pow")},
	}
	factory := operation.NewFactory(nil, nil, callbacks, nil, nil)
	op, err := newHook(factory, hook.Info{Kind: hooks.ConfigChanged})
	c.Assert(err, jc.ErrorIsNil)

	newState, err := op.Commit(operation.State{})
	c.Assert(newState, gc.IsNil)
	c.Assert(err, gc.ErrorMatches, "pow")
}

func (s *RunHookSuite) TestCommitError_Run(c *gc.C) {
	s.testCommitError(c, (operation.Factory).NewRunHook)
}

func (s *RunHookSuite) TestCommitError_Retry(c *gc.C) {
	s.testCommitError(c, (operation.Factory).NewRetryHook)
}

func (s *RunHookSuite) TestCommitError_Skip(c *gc.C) {
	s.testCommitError(c, (operation.Factory).NewSkipHook)
}

func (s *RunHookSuite) testCommitSuccess(c *gc.C, newHook newHook, hookInfo hook.Info, before, after operation.State) {
	callbacks := &CommitHookCallbacks{
		MockCommitHook: &MockCommitHook{},
	}
	factory := operation.NewFactory(nil, nil, callbacks, nil, nil)
	op, err := newHook(factory, hookInfo)
	c.Assert(err, jc.ErrorIsNil)

	newState, err := op.Commit(before)
	c.Assert(err, jc.ErrorIsNil)
	c.Assert(newState, gc.DeepEquals, &after)
}

func (s *RunHookSuite) TestCommitSuccess_ConfigChanged_QueueStartHook(c *gc.C) {
	for i, newHook := range []newHook{
		(operation.Factory).NewRunHook,
		(operation.Factory).NewRetryHook,
		(operation.Factory).NewSkipHook,
	} {
		c.Logf("variant %d", i)
		s.testCommitSuccess(c,
			newHook,
			hook.Info{Kind: hooks.ConfigChanged},
			operation.State{},
			operation.State{
				Kind: operation.RunHook,
				Step: operation.Queued,
				Hook: &hook.Info{Kind: hooks.Start},
			},
		)
	}
}

func (s *RunHookSuite) TestCommitSuccess_ConfigChanged_Preserve(c *gc.C) {
	for i, newHook := range []newHook{
		(operation.Factory).NewRunHook,
		(operation.Factory).NewRetryHook,
		(operation.Factory).NewSkipHook,
	} {
		c.Logf("variant %d", i)
		s.testCommitSuccess(c,
			newHook,
			hook.Info{Kind: hooks.ConfigChanged},
			overwriteState,
			operation.State{
				Started:            true,
				CollectMetricsTime: 1234567,
				Kind:               operation.Continue,
				Step:               operation.Pending,
				Hook:               &hook.Info{Kind: hooks.ConfigChanged},
			},
		)
	}
}

func (s *RunHookSuite) TestCommitSuccess_Start_SetStarted(c *gc.C) {
	for i, newHook := range []newHook{
		(operation.Factory).NewRunHook,
		(operation.Factory).NewRetryHook,
		(operation.Factory).NewSkipHook,
	} {
		c.Logf("variant %d", i)
		s.testCommitSuccess(c,
			newHook,
			hook.Info{Kind: hooks.Start},
			operation.State{},
			operation.State{
				Started: true,
				Kind:    operation.Continue,
				Step:    operation.Pending,
				Hook:    &hook.Info{Kind: hooks.Start},
			},
		)
	}
}

func (s *RunHookSuite) TestCommitSuccess_Start_Preserve(c *gc.C) {
	for i, newHook := range []newHook{
		(operation.Factory).NewRunHook,
		(operation.Factory).NewRetryHook,
		(operation.Factory).NewSkipHook,
	} {
		c.Logf("variant %d", i)
		s.testCommitSuccess(c,
			newHook,
			hook.Info{Kind: hooks.Start},
			overwriteState,
			operation.State{
				Started:            true,
				CollectMetricsTime: 1234567,
				Kind:               operation.Continue,
				Step:               operation.Pending,
				Hook:               &hook.Info{Kind: hooks.Start},
			},
		)
	}
}

func (s *RunHookSuite) testQueueHook_BlankSlate(c *gc.C, cause, effect hooks.Kind) {
	for i, newHook := range []newHook{
		(operation.Factory).NewRunHook,
		(operation.Factory).NewRetryHook,
		(operation.Factory).NewSkipHook,
	} {
		c.Logf("variant %d", i)
		s.testCommitSuccess(c,
			newHook,
			hook.Info{Kind: cause},
			operation.State{},
			operation.State{
				Kind: operation.RunHook,
				Step: operation.Queued,
				Hook: &hook.Info{Kind: effect},
			},
		)
	}
}

func (s *RunHookSuite) testQueueHook_Preserve(c *gc.C, cause, effect hooks.Kind) {
	for i, newHook := range []newHook{
		(operation.Factory).NewRunHook,
		(operation.Factory).NewRetryHook,
		(operation.Factory).NewSkipHook,
	} {
		c.Logf("variant %d", i)
		s.testCommitSuccess(c,
			newHook,
			hook.Info{Kind: cause},
			overwriteState,
			operation.State{
				Kind:               operation.RunHook,
				Step:               operation.Queued,
				Hook:               &hook.Info{Kind: effect},
				Started:            true,
				CollectMetricsTime: 1234567,
			},
		)
	}
}

func (s *RunHookSuite) TestQueueHook_Install_BlankSlate(c *gc.C) {
	s.testQueueHook_BlankSlate(c, hooks.Install, hooks.ConfigChanged)
}

func (s *RunHookSuite) TestQueueHook_Install_Preserve(c *gc.C) {
	s.testQueueHook_Preserve(c, hooks.Install, hooks.ConfigChanged)
}

func (s *RunHookSuite) TestQueueHook_UpgradeCharm_BlankSlate(c *gc.C) {
	s.testQueueHook_BlankSlate(c, hooks.UpgradeCharm, hooks.ConfigChanged)
}

func (s *RunHookSuite) TestQueueHook_UpgradeCharm_Preserve(c *gc.C) {
	s.testQueueHook_Preserve(c, hooks.UpgradeCharm, hooks.ConfigChanged)
}

func (s *RunHookSuite) testQueueNothing_BlankSlate(c *gc.C, hookInfo hook.Info) {
	for i, newHook := range []newHook{
		(operation.Factory).NewRunHook,
		(operation.Factory).NewRetryHook,
		(operation.Factory).NewSkipHook,
	} {
		c.Logf("variant %d", i)
		s.testCommitSuccess(c,
			newHook,
			hookInfo,
			operation.State{},
			operation.State{
				Kind: operation.Continue,
				Step: operation.Pending,
				Hook: &hookInfo,
			},
		)
	}
}

func (s *RunHookSuite) testQueueNothing_Preserve(c *gc.C, hookInfo hook.Info) {
	for i, newHook := range []newHook{
		(operation.Factory).NewRunHook,
		(operation.Factory).NewRetryHook,
		(operation.Factory).NewSkipHook,
	} {
		c.Logf("variant %d", i)
		s.testCommitSuccess(c,
			newHook,
			hookInfo,
			overwriteState,
			operation.State{
				Kind:               operation.Continue,
				Step:               operation.Pending,
				Hook:               &hookInfo,
				Started:            true,
				CollectMetricsTime: 1234567,
			},
		)
	}
}

func (s *RunHookSuite) TestQueueNothing_Stop_BlankSlate(c *gc.C) {
	s.testQueueNothing_BlankSlate(c, hook.Info{
		Kind: hooks.Stop,
	})
}

func (s *RunHookSuite) TestQueueNothing_Stop_Preserve(c *gc.C) {
	s.testQueueNothing_Preserve(c, hook.Info{
		Kind: hooks.Stop,
	})
}

func (s *RunHookSuite) TestQueueNothing_RelationJoined_BlankSlate(c *gc.C) {
	s.testQueueNothing_BlankSlate(c, hook.Info{
		Kind:       hooks.RelationJoined,
		RemoteUnit: "u/0",
	})
}

func (s *RunHookSuite) TestQueueNothing_RelationJoined_Preserve(c *gc.C) {
	s.testQueueNothing_Preserve(c, hook.Info{
		Kind:       hooks.RelationJoined,
		RemoteUnit: "u/0",
	})
}

func (s *RunHookSuite) TestQueueNothing_RelationChanged_BlankSlate(c *gc.C) {
	s.testQueueNothing_BlankSlate(c, hook.Info{
		Kind:       hooks.RelationChanged,
		RemoteUnit: "u/0",
	})
}

func (s *RunHookSuite) TestQueueNothing_RelationChanged_Preserve(c *gc.C) {
	s.testQueueNothing_Preserve(c, hook.Info{
		Kind:       hooks.RelationChanged,
		RemoteUnit: "u/0",
	})
}

func (s *RunHookSuite) TestQueueNothing_RelationDeparted_BlankSlate(c *gc.C) {
	s.testQueueNothing_BlankSlate(c, hook.Info{
		Kind:       hooks.RelationDeparted,
		RemoteUnit: "u/0",
	})
}

func (s *RunHookSuite) TestQueueNothing_RelationDeparted_Preserve(c *gc.C) {
	s.testQueueNothing_Preserve(c, hook.Info{
		Kind:       hooks.RelationDeparted,
		RemoteUnit: "u/0",
	})
}

func (s *RunHookSuite) TestQueueNothing_RelationBroken_BlankSlate(c *gc.C) {
	s.testQueueNothing_BlankSlate(c, hook.Info{
		Kind: hooks.RelationBroken,
	})
}

func (s *RunHookSuite) TestQueueNothing_RelationBroken_Preserve(c *gc.C) {
	s.testQueueNothing_Preserve(c, hook.Info{
		Kind: hooks.RelationBroken,
	})
}

func (s *RunHookSuite) testCommitSuccess_CollectMetricsTime(c *gc.C, newHook newHook) {
	hookInfo := hook.Info{Kind: hooks.CollectMetrics}

	callbacks := &CommitHookCallbacks{
		MockCommitHook: &MockCommitHook{},
	}
	factory := operation.NewFactory(nil, nil, callbacks, nil, nil)
	op, err := newHook(factory, hookInfo)
	c.Assert(err, jc.ErrorIsNil)

	nowBefore := time.Now().Unix()
	newState, err := op.Commit(overwriteState)
	c.Assert(err, jc.ErrorIsNil)

	nowAfter := time.Now().Unix()
	nowWritten := newState.CollectMetricsTime
	c.Logf("%d <= %d <= %d", nowBefore, nowWritten, nowAfter)
	c.Check(nowBefore <= nowWritten, jc.IsTrue)
	c.Check(nowWritten <= nowAfter, jc.IsTrue)

	// Check the other fields match.
	newState.CollectMetricsTime = 0
	c.Check(newState, gc.DeepEquals, &operation.State{
		Started: true,
		Kind:    operation.Continue,
		Step:    operation.Pending,
		Hook:    &hookInfo,
	})
}

func (s *RunHookSuite) TestCommitSuccess_CollectMetricsTime_Run(c *gc.C) {
	s.testCommitSuccess_CollectMetricsTime(c, (operation.Factory).NewRunHook)
}

func (s *RunHookSuite) TestCommitSuccess_CollectMetricsTime_Retry(c *gc.C) {
	s.testCommitSuccess_CollectMetricsTime(c, (operation.Factory).NewRetryHook)
}

func (s *RunHookSuite) TestCommitSuccess_CollectMetricsTime_Skip(c *gc.C) {
	s.testCommitSuccess_CollectMetricsTime(c, (operation.Factory).NewSkipHook)
}
