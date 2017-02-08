// Copyright 2013-2016 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package provisioner_test

import (
	"runtime"

	"github.com/juju/errors"
	gitjujutesting "github.com/juju/testing"
	jc "github.com/juju/testing/checkers"
	"github.com/juju/utils/arch"
	"github.com/juju/version"
	gc "gopkg.in/check.v1"
	"gopkg.in/juju/names.v2"

	"github.com/juju/juju/agent"
	"github.com/juju/juju/api/common"
	"github.com/juju/juju/apiserver/params"
	"github.com/juju/juju/cloudconfig/instancecfg"
	"github.com/juju/juju/constraints"
	"github.com/juju/juju/container"
	"github.com/juju/juju/environs"
	"github.com/juju/juju/instance"
	"github.com/juju/juju/network"
	coretesting "github.com/juju/juju/testing"
	coretools "github.com/juju/juju/tools"
	jujuversion "github.com/juju/juju/version"
	"github.com/juju/juju/worker/provisioner"
)

type lxdBrokerSuite struct {
	coretesting.BaseSuite
	agentConfig agent.ConfigSetterWriter
	api         *fakeAPI
	manager     *fakeContainerManager
}

var _ = gc.Suite(&lxdBrokerSuite{})

func (s *lxdBrokerSuite) SetUpTest(c *gc.C) {
	s.BaseSuite.SetUpTest(c)
	if runtime.GOOS == "windows" {
		c.Skip("Skipping lxd tests on windows")
	}

	// To isolate the tests from the host's architecture, we override it here.
	s.PatchValue(&arch.HostArch, func() string { return arch.AMD64 })

	var err error
	s.agentConfig, err = agent.NewAgentConfig(
		agent.AgentConfigParams{
			Paths:             agent.NewPathsWithDefaults(agent.Paths{DataDir: "/not/used/here"}),
			Tag:               names.NewMachineTag("1"),
			UpgradedToVersion: jujuversion.Current,
			Password:          "dummy-secret",
			Nonce:             "nonce",
			APIAddresses:      []string{"10.0.0.1:1234"},
			CACert:            coretesting.CACert,
			Controller:        coretesting.ControllerTag,
			Model:             coretesting.ModelTag,
		})
	c.Assert(err, jc.ErrorIsNil)
	s.api = NewFakeAPI()
	s.manager = &fakeContainerManager{}
}

func (s *lxdBrokerSuite) startInstance(c *gc.C, broker environs.InstanceBroker, machineId string) (*environs.StartInstanceResult, error) {
	return callStartInstance(c, s, broker, machineId)
}

func (s *lxdBrokerSuite) newLXDBroker(c *gc.C, bridger network.Bridger) (environs.InstanceBroker, error) {
	tag, err := names.ParseMachineTag("machine-1")
	c.Assert(err, jc.ErrorIsNil)
	return provisioner.NewLxdBroker(bridger, tag, s.api, s.manager, s.agentConfig)
}

func (s *lxdBrokerSuite) TestStartInstanceGetObservedNetworkConfigFails(c *gc.C) {
	broker, brokerErr := s.newLXDBroker(c, newFakeBridgerNeverErrors())
	c.Assert(brokerErr, jc.ErrorIsNil)
	s.PatchValue(provisioner.GetObservedNetworkConfig, func(_ common.NetworkConfigSource) ([]params.NetworkConfig, error) {
		return nil, errors.New("TestStartInstanceWithHostNetworkChanges no network")
	})

	machineId := "1/lxd/0"
	_, err := s.startInstance(c, broker, machineId)
	c.Check(err, gc.ErrorMatches, ".*TestStartInstanceWithHostNetworkChanges no network")

	s.api.CheckCalls(c, []gitjujutesting.StubCall{{
		FuncName: "ContainerConfig",
	}, {
		FuncName: "HostChangesForContainer",
		Args:     []interface{}{names.NewMachineTag("1-lxd-0")},
	}})
}

func (s *lxdBrokerSuite) TestStartInstanceWithoutHostNetworkChanges(c *gc.C) {
	broker, brokerErr := s.newLXDBroker(c, newFakeBridgerNeverErrors())
	c.Assert(brokerErr, jc.ErrorIsNil)
	s.PatchValue(provisioner.GetObservedNetworkConfig, func(_ common.NetworkConfigSource) ([]params.NetworkConfig, error) {
		return nil, nil
	})
	machineId := "1/lxd/0"
	s.startInstance(c, broker, machineId)
	s.api.CheckCalls(c, []gitjujutesting.StubCall{{
		FuncName: "ContainerConfig",
	}, {
		FuncName: "HostChangesForContainer",
		Args:     []interface{}{names.NewMachineTag("1-lxd-0")},
	}, {
		FuncName: "PrepareContainerInterfaceInfo",
		Args:     []interface{}{names.NewMachineTag("1-lxd-0")},
	}})
	s.manager.CheckCallNames(c, "CreateContainer")
	call := s.manager.Calls()[0]
	c.Assert(call.Args[0], gc.FitsTypeOf, &instancecfg.InstanceConfig{})
	instanceConfig := call.Args[0].(*instancecfg.InstanceConfig)
	c.Assert(instanceConfig.ToolsList(), gc.HasLen, 1)
	c.Assert(instanceConfig.ToolsList().Arches(), jc.DeepEquals, []string{"amd64"})
}

func (s *lxdBrokerSuite) TestStartInstanceWithHostNetworkChanges(c *gc.C) {
	broker, brokerErr := s.newLXDBroker(c, newFakeBridgerNeverErrors())
	c.Assert(brokerErr, jc.ErrorIsNil)

	observedNetworkConfig := []params.NetworkConfig{
		params.NetworkConfig{
			DeviceIndex:    0,
			MACAddress:     "aa:bb:cc:dd:ee:ff",
			CIDR:           "0.1.2.3/24",
			InterfaceName:  "dummy0",
			Disabled:       false,
			NoAutoStart:    false,
			Address:        "0.1.2.3",
			GatewayAddress: "0.1.2.1",
		},
	}

	s.PatchValue(provisioner.GetObservedNetworkConfig, func(_ common.NetworkConfigSource) ([]params.NetworkConfig, error) {
		return observedNetworkConfig, nil
	})

	machineId := "1/lxd/0"
	s.startInstance(c, broker, machineId)

	s.api.CheckCalls(c, []gitjujutesting.StubCall{{
		FuncName: "ContainerConfig",
	}, {
		FuncName: "HostChangesForContainer",
		Args: []interface{}{
			names.NewMachineTag("1-lxd-0"),
		},
	}, {
		FuncName: "SetHostMachineNetworkConfig",
		Args: []interface{}{
			"machine-1",
			observedNetworkConfig,
		},
	}, {
		FuncName: "PrepareContainerInterfaceInfo",
		Args:     []interface{}{names.NewMachineTag("1-lxd-0")},
	}})
	s.manager.CheckCallNames(c, "CreateContainer")
	call := s.manager.Calls()[0]
	c.Assert(call.Args[0], gc.FitsTypeOf, &instancecfg.InstanceConfig{})
	instanceConfig := call.Args[0].(*instancecfg.InstanceConfig)
	c.Assert(instanceConfig.ToolsList(), gc.HasLen, 1)
	c.Assert(instanceConfig.ToolsList().Arches(), jc.DeepEquals, []string{"amd64"})
}

func (s *lxdBrokerSuite) TestStartInstancePopulatesNetworkInfo(c *gc.C) {
	broker, brokerErr := s.newLXDBroker(c, newFakeBridgerNeverErrors())
	c.Assert(brokerErr, jc.ErrorIsNil)

	patchResolvConf(s, c)

	result, err := s.startInstance(c, broker, "1/lxd/0")
	c.Assert(err, jc.ErrorIsNil)
	c.Assert(result.NetworkInfo, gc.HasLen, 1)
	iface := result.NetworkInfo[0]
	c.Assert(iface, jc.DeepEquals, network.InterfaceInfo{
		DeviceIndex:         0,
		CIDR:                "0.1.2.0/24",
		InterfaceName:       "dummy0",
		ParentInterfaceName: "lxdbr0",
		MACAddress:          "aa:bb:cc:dd:ee:ff",
		Address:             network.NewAddress("0.1.2.3"),
		GatewayAddress:      network.NewAddress("0.1.2.1"),
		DNSServers:          network.NewAddresses("ns1.dummy", "ns2.dummy"),
		DNSSearchDomains:    []string{"dummy", "invalid"},
	})
}

func (s *lxdBrokerSuite) TestStartInstancePopulatesFallbackNetworkInfo(c *gc.C) {
	broker, brokerErr := s.newLXDBroker(c, newFakeBridgerNeverErrors())
	c.Assert(brokerErr, jc.ErrorIsNil)

	s.PatchValue(provisioner.GetObservedNetworkConfig, func(_ common.NetworkConfigSource) ([]params.NetworkConfig, error) {
		return nil, nil
	})

	patchResolvConf(s, c)

	s.api.SetErrors(
		nil, // ContainerConfig succeeds
		nil, // HostChangesForContainer succeeds
		errors.NotSupportedf("container address allocation"),
	)
	result, err := s.startInstance(c, broker, "1/lxd/0")
	c.Assert(err, jc.ErrorIsNil)

	c.Assert(result.NetworkInfo, jc.DeepEquals, []network.InterfaceInfo{{
		DeviceIndex:         0,
		InterfaceName:       "eth0",
		InterfaceType:       network.EthernetInterface,
		ConfigType:          network.ConfigDHCP,
		ParentInterfaceName: "lxdbr0",
		DNSServers:          network.NewAddresses("ns1.dummy", "ns2.dummy"),
		DNSSearchDomains:    []string{"dummy", "invalid"},
	}})
}

func (s *lxdBrokerSuite) TestStartInstanceNoHostArchTools(c *gc.C) {
	broker, brokerErr := s.newLXDBroker(c, newFakeBridgerNeverErrors())
	c.Assert(brokerErr, jc.ErrorIsNil)

	_, err := broker.StartInstance(environs.StartInstanceParams{
		Tools: coretools.List{{
			// non-host-arch tools should be filtered out by StartInstance
			Version: version.MustParseBinary("2.3.4-quantal-arm64"),
			URL:     "http://tools.testing.invalid/2.3.4-quantal-arm64.tgz",
		}},
		InstanceConfig: makeInstanceConfig(c, s, "1/lxd/0"),
	})
	c.Assert(err, gc.ErrorMatches, `need tools for arch amd64, only found \[arm64\]`)
}

type fakeContainerManager struct {
	gitjujutesting.Stub
}

func (m *fakeContainerManager) CreateContainer(instanceConfig *instancecfg.InstanceConfig,
	cons constraints.Value,
	series string,
	network *container.NetworkConfig,
	storage *container.StorageConfig,
	callback container.StatusCallback,
) (instance.Instance, *instance.HardwareCharacteristics, error) {
	m.MethodCall(m, "CreateContainer", instanceConfig, cons, series, network, storage, callback)
	return nil, nil, m.NextErr()
}

func (m *fakeContainerManager) DestroyContainer(id instance.Id) error {
	m.MethodCall(m, "DestroyContainer", id)
	return m.NextErr()
}

func (m *fakeContainerManager) ListContainers() ([]instance.Instance, error) {
	m.MethodCall(m, "ListContainers")
	return nil, m.NextErr()
}

func (m *fakeContainerManager) Namespace() instance.Namespace {
	ns, _ := instance.NewNamespace(coretesting.ModelTag.Id())
	return ns
}

func (m *fakeContainerManager) IsInitialized() bool {
	m.MethodCall(m, "IsInitialized")
	m.PopNoErr()
	return true
}
