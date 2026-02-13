package salesforce

import (
	"github.com/turbot/steampipe-plugin-sdk/v5/plugin"
)

type NamingConventionEnum string

const (
	API_NATIVE NamingConventionEnum = "api_native"
	SNAKE_CASE NamingConventionEnum = "snake_case"
)

type salesforceConfig struct {
	URL              *string               `hcl:"url"`
	Username         *string               `hcl:"username"`
	Password         *string               `hcl:"password"`
	Token            *string               `hcl:"token"`
	AccessToken      *string               `hcl:"access_token"`
	RefreshToken     *string               `hcl:"refresh_token"`
	ClientSecret     *string               `hcl:"client_secret"`
	PrivateKey       *string               `hcl:"private_key"`
	PrivateKeyFile   *string               `hcl:"private_key_file"`
	ClientId         *string               `hcl:"client_id"`
	APIVersion       *string               `hcl:"api_version"`
	Objects          *[]string             `hcl:"objects"`
	NamingConvention *NamingConventionEnum `hcl:"naming_convention"`
}

func ConfigInstance() interface{} {
	return &salesforceConfig{}
}

// GetConfig :: retrieve and cast connection config from query data
func GetConfig(connection *plugin.Connection) salesforceConfig {
	if connection == nil || connection.Config == nil {
		return salesforceConfig{}
	}
	config, _ := connection.Config.(salesforceConfig)
	return config
}
