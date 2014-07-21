// Copyright 2013 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package apiserver

import (
	"reflect"
	"sync"
	"time"

	"github.com/juju/errors"
	"github.com/juju/names"
	"github.com/juju/utils/set"

	"github.com/juju/juju/rpc"
	"github.com/juju/juju/rpc/rpcreflect"
	"github.com/juju/juju/state"
	"github.com/juju/juju/state/api/params"
	"github.com/juju/juju/state/apiserver/common"
)

type taggedAuthenticator interface {
	state.Entity
	state.Authenticator
}

var (
	// maxClientPingInterval defines the timeframe until the ping timeout
	// closes the monitored connection. TODO(mue): Idea by Roger:
	// Move to API (e.g. params) so that the pinging there may
	// depend on the interval.
	maxClientPingInterval = 3 * time.Minute

	// mongoPingInterval defines the interval at which an API server
	// will ping the mongo session to make sure that it's still
	// alive. When the ping returns an error, the server will be
	// terminated.
	mongoPingInterval = 10 * time.Second
)

type objectKey struct {
	name    string
	version int
	objId   string
}

// ApiHandler represents a single client's connection to the state
// after it has logged in. It contains an rpc.MethodFinder which it
// uses to dispatch Api calls appropriately.
type ApiHandler struct {
	rpc.MethodFinder
	state     *state.State
	rpcConn   *rpc.Conn
	resources *common.Resources
	entity    state.Entity
}

var _ = (*ApiHandler)(nil)

// NewApiHandler returns a new ApiHandler.
func NewApiHandler(srv *Server, rpcConn *rpc.Conn) *ApiHandler {
	r := &ApiHandler{
		state:     srv.state,
		resources: common.NewResources(),
		rpcConn:   rpcConn,
	}
	r.resources.RegisterNamed("dataDir", common.StringResource(srv.dataDir))
	return r
}

func (r *ApiHandler) getResources() *common.Resources {
	return r.resources
}

func (r *ApiHandler) getRpcConn() *rpc.Conn {
	return r.rpcConn
}

// Kill implements rpc.Killer.  It cleans up any resources that need
// cleaning up to ensure that all outstanding requests return.
func (r *ApiHandler) Kill() {
	r.resources.StopAll()
}

// srvCaller is our implementation of the rpcreflect.MethodCaller interface.
// It lives just long enough to encapsulate the methods that should be
// available for an RPC call and allow the RPC code to instantiate an object
// and place a call on its method.
type srvCaller struct {
	objMethod rpcreflect.ObjMethod
	goType    reflect.Type
	creator   func(id string) (reflect.Value, error)
}

// ParamsType defines the parameters that should be supplied to this function.
// See rpcreflect.MethodCaller for more detail.
func (s *srvCaller) ParamsType() reflect.Type {
	return s.objMethod.Params
}

// ReturnType defines the object that is returned from the function.`
// See rpcreflect.MethodCaller for more detail.
func (s *srvCaller) ResultType() reflect.Type {
	return s.objMethod.Result
}

// Call takes the object Id and an instance of ParamsType to create an object and place
// a call on its method. It then returns an instance of ResultType.
func (s *srvCaller) Call(objId string, arg reflect.Value) (reflect.Value, error) {
	objVal, err := s.creator(objId)
	if err != nil {
		return reflect.Value{}, err
	}
	return s.objMethod.Call(objVal, arg)
}

// ApiFilter restricts API calls to an underlying rpc.MethodFinder
// to those which match a collection of root names.
type ApiFilter struct {
	rpc.MethodFinder
	filter map[string]bool
}

// NewApiFilter returns a new ApiFilter that only allows the given root names
// through to the underlying MethodFinder.
func NewApiFilter(finder rpc.MethodFinder, rootNames ...string) *ApiFilter {
	r := &ApiFilter{
		MethodFinder: finder,
		filter:       make(map[string]bool),
	}
	for _, rootName := range rootNames {
		r.filter[rootName] = true
	}
	return r
}

func (r *ApiFilter) FindMethod(rootName string, version int, methodName string) (rpcreflect.MethodCaller, error) {
	if !r.filter[rootName] {
		return nil, &rpcreflect.CallNotImplementedError{
			RootMethod: rootName,
			Version:    version,
			Method:     methodName,
		}
	}
	return r.MethodFinder.FindMethod(rootName, version, methodName)
}

// ApiRoot implements basic method dispatching to the facade registry.
type ApiRoot struct {
	state       *state.State
	resources   *common.Resources
	authorizer  common.Authorizer
	objectMutex sync.RWMutex
	objectCache map[objectKey]reflect.Value
}

// NewApiRoot returns a new ApiRoot.
func NewApiRoot(srv *Server, resources *common.Resources, authorizer common.Authorizer) *ApiRoot {
	r := &ApiRoot{
		state:       srv.state,
		resources:   resources,
		authorizer:  authorizer,
		objectCache: make(map[objectKey]reflect.Value),
	}
	return r
}

// FindMethod looks up the given rootName and version in our facade registry
// and returns a MethodCaller that will be used by the RPC code to place calls on
// that facade.
// FindMethod uses the global registry state/apiserver/common.Facades.
// For more information about how FindMethod should work, see rpc/server.go and
// rpc/rpcreflect/value.go
func (r *ApiRoot) FindMethod(rootName string, version int, methodName string) (rpcreflect.MethodCaller, error) {
	goType, objMethod, err := r.lookupMethod(rootName, version, methodName)
	if err != nil {
		return nil, err
	}

	creator := func(id string) (reflect.Value, error) {
		objKey := objectKey{name: rootName, version: version, objId: id}
		r.objectMutex.RLock()
		objValue, ok := r.objectCache[objKey]
		r.objectMutex.RUnlock()
		if ok {
			return objValue, nil
		}
		r.objectMutex.Lock()
		defer r.objectMutex.Unlock()
		if objValue, ok := r.objectCache[objKey]; ok {
			return objValue, nil
		}
		// Now that we have the write lock, check one more time in case
		// someone got the write lock before us.
		factory, err := common.Facades.GetFactory(rootName, version)
		if err != nil {
			// We don't check for IsNotFound here, because it
			// should have already been handled in the GetType
			// check.
			return reflect.Value{}, err
		}
		obj, err := factory(r.state, r.resources, r.authorizer, id)
		if err != nil {
			return reflect.Value{}, err
		}
		objValue = reflect.ValueOf(obj)
		if !objValue.Type().AssignableTo(goType) {
			return reflect.Value{}, errors.Errorf(
				"internal error, %s(%d) claimed to return %s but returned %T",
				rootName, version, goType, obj)
		}
		if goType.Kind() == reflect.Interface {
			// If the original function wanted to return an
			// interface type, the indirection in the factory via
			// an interface{} strips the original interface
			// information off. So here we have to create the
			// interface again, and assign it.
			asInterface := reflect.New(goType).Elem()
			asInterface.Set(objValue)
			objValue = asInterface
		}
		r.objectCache[objKey] = objValue
		return objValue, nil
	}
	return &srvCaller{
		creator:   creator,
		objMethod: objMethod,
	}, nil
}

func (r *ApiRoot) lookupMethod(rootName string, version int, methodName string) (reflect.Type, rpcreflect.ObjMethod, error) {
	noMethod := rpcreflect.ObjMethod{}
	goType, err := common.Facades.GetType(rootName, version)
	if err != nil {
		if errors.IsNotFound(err) {
			return nil, noMethod, &rpcreflect.CallNotImplementedError{
				RootMethod: rootName,
				Version:    version,
			}
		}
		return nil, noMethod, err
	}
	rpcType := rpcreflect.ObjTypeOf(goType)
	objMethod, err := rpcType.Method(methodName)
	if err != nil {
		if err == rpcreflect.ErrMethodNotFound {
			return nil, noMethod, &rpcreflect.CallNotImplementedError{
				RootMethod: rootName,
				Version:    version,
				Method:     methodName,
			}
		}
		return nil, noMethod, err
	}
	return goType, objMethod, nil
}

// AnonRoot dispatches API calls to those available to an anonymous connection
// which has not logged in.
type AnonRoot struct {
	rpc.MethodFinder
	srv         *Server
	adminApi    interface{}
	reqNotifier *requestNotifier
}

// NewAnonRoot creates a new AnonRoot which dispatches to the given Admin API implementation.
func NewAnonRoot(srv *Server, adminApi interface{}) *AnonRoot {
	r := &AnonRoot{
		srv:      srv,
		adminApi: adminApi,
	}
	r.MethodFinder = NewApiFilter(r, "Admin")
	return r
}

func (r *AnonRoot) FindMethod(rootName string, version int, methodName string) (rpcreflect.MethodCaller, error) {
	return rpcreflect.ValueOf(reflect.ValueOf(r.adminApi)).FindMethod(rootName, version, methodName)
}

// UpgradingRoot restricts API calls to those supported during an upgrade.
type UpgradingRoot struct {
	rpc.MethodFinder
}

// NewUpgradingRoot returns a new UpgradingRoot.
func NewUpgradingRoot(finder rpc.MethodFinder) *UpgradingRoot {
	return &UpgradingRoot{finder}
}

var inUpgradeError = errors.New("upgrade in progress - Juju functionality is limited")

var allowedMethodsDuringUpgrades = set.NewStrings(
	"Client.FullStatus",     // for "juju status"
	"Client.PrivateAddress", // for "juju ssh"
	"Client.PublicAddress",  // for "juju ssh"
	"Client.WatchDebugLog",  // for "juju debug-log"
)

func isMethodAllowedDuringUpgrade(rootName, methodName string) bool {
	fullName := rootName + "." + methodName
	return allowedMethodsDuringUpgrades.Contains(fullName)
}

func (r *UpgradingRoot) FindMethod(rootName string, version int, methodName string) (rpcreflect.MethodCaller, error) {
	if !isMethodAllowedDuringUpgrade(rootName, methodName) {
		return nil, inUpgradeError
	}
	return r.MethodFinder.FindMethod(rootName, version, methodName)
}

// AuthMachineAgent returns whether the current client is a machine agent.
func (r *ApiHandler) AuthMachineAgent() bool {
	_, ok := r.entity.(*state.Machine)
	return ok
}

// AuthUnitAgent returns whether the current client is a unit agent.
func (r *ApiHandler) AuthUnitAgent() bool {
	_, ok := r.entity.(*state.Unit)
	return ok
}

// AuthOwner returns whether the authenticated user's tag matches the
// given entity tag.
func (r *ApiHandler) AuthOwner(tag string) bool {
	return r.entity.Tag().String() == tag
}

// AuthEnvironManager returns whether the authenticated user is a
// machine with running the ManageEnviron job.
func (r *ApiHandler) AuthEnvironManager() bool {
	return isMachineWithJob(r.entity, state.JobManageEnviron)
}

// AuthClient returns whether the authenticated entity is a client
// user.
func (r *ApiHandler) AuthClient() bool {
	_, isUser := r.entity.(*state.User)
	return isUser
}

// GetAuthTag returns the tag of the authenticated entity.
func (r *ApiHandler) GetAuthTag() names.Tag {
	return r.entity.Tag()
}

// GetAuthEntity returns the authenticated entity.
func (r *ApiHandler) GetAuthEntity() state.Entity {
	return r.entity
}

// DescribeFacades returns the list of available Facades and their Versions
func (r *ApiHandler) DescribeFacades() []params.FacadeVersions {
	facades := common.Facades.List()
	result := make([]params.FacadeVersions, len(facades))
	for i, facade := range facades {
		result[i].Name = facade.Name
		result[i].Versions = facade.Versions
	}
	return result
}
