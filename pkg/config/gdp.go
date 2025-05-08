package config

import (
	"github.com/squat/generic-device-plugin/deviceplugin"
)

type GenericDevicePlugin struct {
	// Enabled or Disabled
	Status string `json:"status"`

	// device.microshift.io
	Domain string `json:"domain"`

	Devices []deviceplugin.DeviceSpec `json:"devices"`
}

// TODO: Validation

func genericDevicePluginDefaults() GenericDevicePlugin {
	return GenericDevicePlugin{
		Status: "Disabled",
		Domain: "device.microshift.io",
		Devices: []deviceplugin.DeviceSpec{
			{
				Name: "serial",
				Groups: []*deviceplugin.Group{
					{Paths: []*deviceplugin.Path{{Path: "/dev/ttyUSB*"}}},
					{Paths: []*deviceplugin.Path{{Path: "/dev/ttyUSB*"}}},
					{Paths: []*deviceplugin.Path{{Path: "/dev/ttyACM*"}}},
					{Paths: []*deviceplugin.Path{{Path: "/dev/tty.usb*"}}},
					{Paths: []*deviceplugin.Path{{Path: "/dev/cu.*"}}},
					{Paths: []*deviceplugin.Path{{Path: "/dev/cuaU*"}}},
					{Paths: []*deviceplugin.Path{{Path: "/dev/rfcomm*"}}},
				},
			},
			{
				Name: "video",
				Groups: []*deviceplugin.Group{
					{
						Paths: []*deviceplugin.Path{{Path: "/dev/video0"}},
					},
				},
			},
			{
				Name: "fuse",
				Groups: []*deviceplugin.Group{
					{
						Paths: []*deviceplugin.Path{{Path: "/dev/fuse"}},
						Count: 10,
					},
				},
			},
			{
				Name: "audio",
				Groups: []*deviceplugin.Group{
					{
						Paths: []*deviceplugin.Path{{Path: "/dev/snd"}},
						Count: 10,
					},
				},
			},
			{
				Name: "capture",
				Groups: []*deviceplugin.Group{
					{
						Paths: []*deviceplugin.Path{
							{Path: "/dev/snd/controlC0"},
							{Path: "/dev/snd/pcmC0D0c"},
						},
					},

					{
						Paths: []*deviceplugin.Path{
							{Path: "/dev/snd/controlC1", MountPath: "/dev/snd/controlC0"},
							{Path: "/dev/snd/pcmC1D0c", MountPath: "/dev/snd/pcmC0D0c"},
						},
					},
					{
						Paths: []*deviceplugin.Path{
							{Path: "/dev/snd/controlC2", MountPath: "/dev/snd/controlC0"},
							{Path: "/dev/snd/pcmC2D0c", MountPath: "/dev/snd/pcmC0D0c"},
						},
					},
					{
						Paths: []*deviceplugin.Path{
							{Path: "/dev/snd/controlC3", MountPath: "/dev/snd/controlC0"},
							{Path: "/dev/snd/pcmC3D0c", MountPath: "/dev/snd/pcmC0D0c"},
						},
					},
				},
			},
		},
	}
}
