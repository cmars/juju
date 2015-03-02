// Copyright 2014 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package state_test

import (
	"time"

	jc "github.com/juju/testing/checkers"
	gc "gopkg.in/check.v1"
	"gopkg.in/mgo.v2/bson"

	"github.com/juju/juju/state"
	"github.com/juju/juju/testing"
	"github.com/juju/juju/testing/factory"
)

type MeterStateSuite struct {
	ConnSuite
	unit    *state.Unit
	factory *factory.Factory
}

var _ = gc.Suite(&MeterStateSuite{})

func (s *MeterStateSuite) SetUpTest(c *gc.C) {
	s.ConnSuite.SetUpTest(c)
	s.factory = factory.NewFactory(s.State)
	s.unit = s.factory.MakeUnit(c, nil)
	c.Assert(s.unit.Series(), gc.Equals, "quantal")
}

func (s *UnitSuite) TestMeterStatus(c *gc.C) {
	status, info, err := s.unit.GetMeterStatus()
	c.Assert(status, gc.Equals, "NOT SET")
	c.Assert(info, gc.Equals, "")
	c.Assert(err, jc.ErrorIsNil)
	err = s.unit.SetMeterStatus("GREEN", "Additional information.")
	c.Assert(err, jc.ErrorIsNil)
	status, info, err = s.unit.GetMeterStatus()
	c.Assert(status, gc.Equals, "GREEN")
	c.Assert(info, gc.Equals, "Additional information.")
	c.Assert(err, jc.ErrorIsNil)
}

func (s *UnitSuite) TestMeterStatusIncludesEnvUUID(c *gc.C) {
	jujuDB := s.MgoSuite.Session.DB("juju")
	meterStatus := jujuDB.C("meterStatus")
	var docs []bson.M
	err := meterStatus.Find(nil).All(&docs)
	c.Assert(err, jc.ErrorIsNil)
	c.Assert(docs, gc.HasLen, 1)
	c.Assert(docs[0]["env-uuid"], gc.Equals, s.State.EnvironUUID())
}

func (s *UnitSuite) TestSetMeterStatusIncorrect(c *gc.C) {
	err := s.unit.SetMeterStatus("NOT SET", "Additional information.")
	c.Assert(err, gc.NotNil)
	status, info, err := s.unit.GetMeterStatus()
	c.Assert(status, gc.Equals, "NOT SET")
	c.Assert(info, gc.Equals, "")
	c.Assert(err, jc.ErrorIsNil)

	err = s.unit.SetMeterStatus("this-is-not-a-valid-status", "Additional information.")
	c.Assert(err, gc.NotNil)
	status, info, err = s.unit.GetMeterStatus()
	c.Assert(status, gc.Equals, "NOT SET")
	c.Assert(info, gc.Equals, "")
	c.Assert(err, jc.ErrorIsNil)
}

func (s *UnitSuite) TestSetMeterStatusWhenDying(c *gc.C) {
	preventUnitDestroyRemove(c, s.unit)
	testWhenDying(c, s.unit, contentionErr, contentionErr, func() error {
		err := s.unit.SetMeterStatus("GREEN", "Additional information.")
		if err != nil {
			return err
		}
		status, info, err := s.unit.GetMeterStatus()
		c.Assert(status, gc.Equals, "NOT SET")
		c.Assert(info, gc.Equals, "")
		c.Assert(err, jc.ErrorIsNil)
		return nil
	})
}

func (s *UnitSuite) TestMeterStatusRemovedWithUnit(c *gc.C) {
	err := s.unit.SetMeterStatus("GREEN", "Information.")
	c.Assert(err, jc.ErrorIsNil)
	err = s.unit.EnsureDead()
	c.Assert(err, jc.ErrorIsNil)
	err = s.unit.Remove()
	c.Assert(err, jc.ErrorIsNil)
	code, info, err := s.unit.GetMeterStatus()
	c.Assert(err, gc.ErrorMatches, "cannot retrieve meter status for unit .*: not found")
	c.Assert(code, gc.Equals, "NOT AVAILABLE")
	c.Assert(info, gc.Equals, "")
}

func (s *MeterStateSuite) TestMeterStatusWatcherRespondstoMeterStatus(c *gc.C) {
	watcher := s.unit.WatchMeterStatus()
	<-watcher.Changes() // After creation there is an item in the channel, let's get rid of it
	start := make(chan struct{})
	go func() {
		err := s.unit.SetMeterStatus("GREEN", "Information.")
		c.Assert(err, jc.ErrorIsNil)
		<-start
	}()
	start <- struct{}{}
	select {
	case <-watcher.Changes():
	case <-time.After(testing.LongWait):
		c.Fatalf("expected event from watcher by now")
	}
}

func (s *MeterStateSuite) TestMeterStatusWatcherRespondsToMetricsManager(c *gc.C) {
	mm, err := s.State.NewMetricsManager()
	c.Assert(err, jc.ErrorIsNil)
	code, info := mm.MeterStatus()
	watcher := s.unit.WatchMeterStatus()
	<-watcher.Changes()
	start := make(chan struct{})
	go func() {
		err := mm.SetMetricsManagerSuccessfulSend(time.Now())
		c.Assert(err, jc.ErrorIsNil)
		for i := 0; i < 3; i++ {
			err := mm.IncrementConsecutiveErrors()
			c.Assert(err, jc.ErrorIsNil)
		}
		code, info = mm.MeterStatus()
		c.Assert(code, gc.Equals, "AMBER") // Confirm meter status has changed
		<-start
	}()
	start <- struct{}{}
	select {
	case <-watcher.Changes():
	case <-time.After(testing.LongWait):
		c.Fatalf("expected event from watcher by now")
	}
}
