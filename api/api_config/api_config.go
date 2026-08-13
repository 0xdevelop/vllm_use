package api_config

// ApiConfig retains the protocol-neutral result encryption switch used by the
// shared executor. Listener and product settings live in ability_settings.
type ApiConfig struct {
	NeedEncryption bool `yaml:"need_encryption" json:"need_encryption" toml:"need_encryption"`
}

var CurrentApiCfg *ApiConfig
