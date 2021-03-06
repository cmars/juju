// Copyright 2015 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package storage

import "github.com/juju/names"

// Volume describes a volume (disk, logical volume, etc.)
type Volume struct {
	// Name is a unique name assigned by Juju to the volume.
	Tag names.VolumeTag

	// VolumeId is a unique provider-supplied ID for the volume.
	// VolumeId is required to be unique for the lifetime of the
	// volume, but may be reused.
	VolumeId string

	// Serial is the volume's serial number. Not all volumes have a serial
	// number, so this may be left blank.
	Serial string

	// Size is the size of the volume, in MiB.
	Size uint64

	// TODO(axw) record volume persistence
}

// VolumeAttachment describes machine-specific volume attachment information,
// including how the volume is exposed on the machine.
type VolumeAttachment struct {
	// Volume is the unique tag assigned by Juju for the volume
	// that this attachment corresponds to.
	Volume names.VolumeTag

	// Machine is the unique tag assigned by Juju for the machine that
	// this attachment corresponds to.
	Machine names.MachineTag

	// DeviceName is the volume's OS-specific device name (e.g. "sdb").
	//
	// If the device name may change (e.g. on machine restart), then this
	// field must be left blank.
	DeviceName string

	// ReadOnly signifies whether the volume is read only or writable.
	ReadOnly bool
}
