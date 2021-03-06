// Copyright 2015 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package openstack

import (
	"github.com/juju/juju/environs"
	"github.com/juju/juju/environs/tools"
	"github.com/juju/juju/storage/provider/registry"
)

const (
	providerType = "openstack"
)

func init() {
	environs.RegisterProvider(providerType, environProvider{})
	environs.RegisterImageDataSourceFunc("keystone catalog", getKeystoneImageSource)
	tools.RegisterToolsDataSourceFunc("keystone catalog", getKeystoneToolsSource)

	registry.RegisterEnvironStorageProviders(providerType)
}
