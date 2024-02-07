package config

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/containernetworking/cni/pkg/types"
)

const (
	DefaultSubnetFile = "/run/simple-cni-plugin/subnet.json"
	DefaultBridgeName = "cni0"
)

type SubnetConf struct {
	Subnet string `json:"subnet"`
	Bridge string `json:"bridge"`
}

func LoadSubnetConfig() (*SubnetConf, error) {
	data, err := os.ReadFile(DefaultSubnetFile)
	if err != nil {
		return nil, err
	}

	conf := &SubnetConf{}
	if err = json.Unmarshal(data, conf); err != nil {
		return nil, err
	}

	return conf, nil
}

func StoreSubnetConfig(conf *SubnetConf) error {
	data, err := json.Marshal(conf)
	if err != nil {
		return err
	}

	return os.WriteFile(DefaultSubnetFile, data, 0644)
}

type PluginConf struct {
	types.NetConf

	RuntimeConf *struct {
		Config map[string]interface{} `json:"config"`
	} `json:"runtimeConf,omitempty"`

	Args *struct {
		A map[string]interface{} `json:"cni"`
	} `json:"args"`

	DataDir string `json:"dataDir"`
}

func parsePluginConf(stdin []byte) (*PluginConf, error) {
	conf := &PluginConf{}
	if err := json.Unmarshal(stdin, conf); err != nil {
		return nil, fmt.Errorf("failed to parse network configuration: %v", err)
	}
	return conf, nil
}

type CNIConf struct {
	PluginConf
	SubnetConf
}

func LoadCNIConfig(stdin []byte) (*CNIConf, error) {
	pluginConf, err := parsePluginConf(stdin)
	if err != nil {
		return nil, err
	}

	subnetConf, err := LoadSubnetConfig()
	if err != nil {
		return nil, err
	}

	return &CNIConf{
		PluginConf: *pluginConf,
		SubnetConf: *subnetConf,
	}, nil
}
