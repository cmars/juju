// Copyright 2015 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package storageprovisioner

import (
	"github.com/juju/errors"
	"github.com/juju/loggo"
	"github.com/juju/names"

	"github.com/juju/juju/apiserver/common"
	"github.com/juju/juju/apiserver/params"
	"github.com/juju/juju/state"
	"github.com/juju/juju/state/watcher"
	"github.com/juju/juju/storage/poolmanager"
)

var logger = loggo.GetLogger("juju.apiserver.storageprovisioner")

func init() {
	common.RegisterStandardFacade("StorageProvisioner", 1, NewStorageProvisionerAPI)
}

// StorageProvisionerAPI provides access to the Provisioner API facade.
type StorageProvisionerAPI struct {
	*common.LifeGetter
	*common.DeadEnsurer

	st                 provisionerState
	settings           poolmanager.SettingsManager
	resources          *common.Resources
	authorizer         common.Authorizer
	getMachineAuthFunc common.GetAuthFunc
	getVolumeAuthFunc  common.GetAuthFunc
}

var getState = func(st *state.State) provisionerState {
	return stateShim{st}
}

var getSettingsManager = func(st *state.State) poolmanager.SettingsManager {
	return state.NewStateSettings(st)
}

// NewStorageProvisionerAPI creates a new server-side StorageProvisionerAPI facade.
func NewStorageProvisionerAPI(st *state.State, resources *common.Resources, authorizer common.Authorizer) (*StorageProvisionerAPI, error) {
	if !authorizer.AuthMachineAgent() {
		return nil, common.ErrPerm
	}
	isEnvironManager := authorizer.AuthEnvironManager()
	authEntityTag := authorizer.GetAuthTag()
	getMachineAuthFunc := func() (common.AuthFunc, error) {
		return func(tag names.Tag) bool {
			switch tag := tag.(type) {
			case names.EnvironTag:
				// Environment managers can access all volumes
				// scoped to the environment.
				return isEnvironManager
			case names.MachineTag:
				if tag == authEntityTag {
					// Machine agents can access volumes
					// scoped to their own machine.
					return true
				}
				parentId := state.ParentId(tag.Id())
				if parentId == "" {
					return false
				}
				// All containers with the authenticated
				// machine as a parent are accessible by it.
				return names.NewMachineTag(parentId) == authEntityTag
			default:
				return false
			}
		}, nil
	}
	getVolumeAuthFunc := func() (common.AuthFunc, error) {
		return func(tag names.Tag) bool {
			switch tag.(type) {
			case names.VolumeTag:
				// TODO(axw) volume tag should include machine
				// scope, which we can then use for authentication
				// and watching purposes.
				return true
			default:
				return false
			}
		}, nil
	}
	stateInterface := getState(st)
	settings := getSettingsManager(st)
	return &StorageProvisionerAPI{
		LifeGetter:         common.NewLifeGetter(stateInterface, getVolumeAuthFunc),
		DeadEnsurer:        common.NewDeadEnsurer(stateInterface, getVolumeAuthFunc),
		st:                 stateInterface,
		settings:           settings,
		resources:          resources,
		authorizer:         authorizer,
		getMachineAuthFunc: getMachineAuthFunc,
		getVolumeAuthFunc:  getVolumeAuthFunc,
	}, nil
}

// WatchVolumes watches for changes to volumes scoped to the
// entity with the tag passed to NewState.
func (s *StorageProvisionerAPI) WatchVolumes(args params.Entities) (params.StringsWatchResults, error) {
	canAccess, err := s.getMachineAuthFunc()
	if err != nil {
		return params.StringsWatchResults{}, common.ServerError(common.ErrPerm)
	}
	results := params.StringsWatchResults{
		Results: make([]params.StringsWatchResult, len(args.Entities)),
	}
	one := func(arg params.Entity) (string, []string, error) {
		tag, err := names.ParseTag(arg.Tag)
		if err != nil || !canAccess(tag) {
			return "", nil, common.ErrPerm
		}
		// TODO(axw) record a scope for volumes, and watch
		// only volumes in that scope.
		w := s.st.WatchVolumes()
		if changes, ok := <-w.Changes(); ok {
			return s.resources.Register(w), changes, nil
		}
		return "", nil, watcher.EnsureErr(w)
	}
	for i, arg := range args.Entities {
		var result params.StringsWatchResult
		id, changes, err := one(arg)
		if err != nil {
			result.Error = common.ServerError(err)
		} else {
			result.StringsWatcherId = id
			result.Changes = changes
		}
		results.Results[i] = result
	}
	return results, nil
}

// Volumes returns details of volumes with the specified tags.
func (s *StorageProvisionerAPI) Volumes(args params.Entities) (params.VolumeResults, error) {
	canAccess, err := s.getVolumeAuthFunc()
	if err != nil {
		return params.VolumeResults{}, common.ServerError(common.ErrPerm)
	}
	results := params.VolumeResults{
		Results: make([]params.VolumeResult, len(args.Entities)),
	}
	one := func(arg params.Entity) (params.Volume, error) {
		tag, err := names.ParseVolumeTag(arg.Tag)
		if err != nil || !canAccess(tag) {
			return params.Volume{}, common.ErrPerm
		}
		volume, err := s.st.Volume(tag)
		if errors.IsNotFound(err) {
			return params.Volume{}, common.ErrPerm
		} else if err != nil {
			return params.Volume{}, err
		}
		return common.VolumeFromState(volume)
	}
	for i, arg := range args.Entities {
		var result params.VolumeResult
		volume, err := one(arg)
		if err != nil {
			result.Error = common.ServerError(err)
		} else {
			result.Result = volume
		}
		results.Results[i] = result
	}
	return results, nil
}

// VolumeParams returns the parameters for creating the volumes
// with the specified tags.
func (s *StorageProvisionerAPI) VolumeParams(args params.Entities) (params.VolumeParamsResults, error) {
	canAccess, err := s.getVolumeAuthFunc()
	if err != nil {
		return params.VolumeParamsResults{}, err
	}
	results := params.VolumeParamsResults{
		Results: make([]params.VolumeParamsResult, len(args.Entities)),
	}
	poolManager := poolmanager.New(s.settings)
	one := func(arg params.Entity) (params.VolumeParams, error) {
		tag, err := names.ParseVolumeTag(arg.Tag)
		if err != nil || !canAccess(tag) {
			return params.VolumeParams{}, common.ErrPerm
		}
		volume, err := s.st.Volume(tag)
		if errors.IsNotFound(err) {
			return params.VolumeParams{}, common.ErrPerm
		} else if err != nil {
			return params.VolumeParams{}, err
		}
		volumeAttachments, err := s.st.VolumeAttachments(tag)
		if err != nil {
			return params.VolumeParams{}, err
		}
		volumeParams, err := common.VolumeParams(volume, poolManager)
		if err != nil {
			return params.VolumeParams{}, err
		}
		if len(volumeAttachments) == 1 {
			volumeParams.MachineTag = volumeAttachments[0].Machine().String()
		}
		return volumeParams, nil
	}
	for i, arg := range args.Entities {
		var result params.VolumeParamsResult
		volumeParams, err := one(arg)
		if err != nil {
			result.Error = common.ServerError(err)
		} else {
			result.Result = volumeParams
		}
		results.Results[i] = result
	}
	return results, nil
}

// SetVolumeInfo records the details of newly provisioned volumes.
func (s *StorageProvisionerAPI) SetVolumeInfo(args params.Volumes) (params.ErrorResults, error) {
	canAccessVolume, err := s.getVolumeAuthFunc()
	if err != nil {
		return params.ErrorResults{}, err
	}
	results := params.ErrorResults{
		Results: make([]params.ErrorResult, len(args.Volumes)),
	}
	one := func(arg params.Volume) error {
		volumeTag, volumeInfo, err := common.VolumeToState(arg)
		if err != nil {
			return errors.Trace(err)
		} else if !canAccessVolume(volumeTag) {
			return common.ErrPerm
		}
		err = s.st.SetVolumeInfo(volumeTag, volumeInfo)
		if errors.IsNotFound(err) {
			return common.ErrPerm
		}
		return errors.Trace(err)
	}
	for i, arg := range args.Volumes {
		err := one(arg)
		results.Results[i].Error = common.ServerError(err)
	}
	return results, nil
}
