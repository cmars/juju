// Copyright 2015 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package uniter

import (
	"github.com/juju/errors"
	"github.com/juju/names"

	"github.com/juju/juju/apiserver/common"
	"github.com/juju/juju/apiserver/params"
	"github.com/juju/juju/state"
)

// StatusAPI is the uniter part that deals with setting/getting
// status from different entities, this particular separation from
// base is because we have a shim to support unit/agent split.
type StatusAPI struct {
	agentSetter  *common.StatusSetter
	unitSetter   *common.StatusSetter
	getCanModify common.GetAuthFunc
}

type unitAgentFinder struct {
	state.EntityFinder
}

func (ua *unitAgentFinder) FindEntity(tag names.Tag) (state.Entity, error) {
	_, ok := tag.(names.UnitTag)
	if !ok {
		return nil, errors.Errorf("unsupported tag %T", tag)
	}
	entity, err := ua.EntityFinder.FindEntity(tag)
	if err != nil {
		return nil, errors.Trace(err)
	}
	return entity.(*state.Unit).Agent(), nil
}

// NewStatusAPI creates a new server-side Status setter API facade.
func NewStatusAPI(st *state.State, getCanModify common.GetAuthFunc) *StatusAPI {
	unitSetter := common.NewStatusSetter(st, getCanModify)
	agentSetter := common.NewStatusSetter(&unitAgentFinder{st}, getCanModify)
	return &StatusAPI{
		agentSetter:  agentSetter,
		unitSetter:   unitSetter,
		getCanModify: getCanModify,
	}
}

// SetStatus will set status for a entities passed in args. If the entity is
// a Unit it will instead set status to its agent, to emulate backwards
// compatibility.
func (s *StatusAPI) SetStatus(args params.SetStatus) (params.ErrorResults, error) {
	return s.SetAgentStatus(args)
}

// SetAgentStatus will set status for agents of Units passed in args, if one
// of the args is not an Unit it will fail.
func (s *StatusAPI) SetAgentStatus(args params.SetStatus) (params.ErrorResults, error) {
	return s.agentSetter.SetStatus(args)
}

// SetUnitStatus sets status for all elements passed in args, the difference
// with SetStatus is that if an entity is a Unit it will set its status instead
// of its agent.
func (s *StatusAPI) SetUnitStatus(args params.SetStatus) (params.ErrorResults, error) {
	return s.unitSetter.SetStatus(args)
}
