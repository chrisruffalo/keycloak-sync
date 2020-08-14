package sync

import (
	"github.com/go-playground/validator/v10"
	"github.com/spf13/viper"
)

type AuthClientConfig struct {
	ClientId            string  			`mapstructure:"client-id" validate:"required"`
	ClientSecret        string  			`mapstructure:"client-secret" validate:"required"`
}

type AuthUserConfig struct {
	Username            string				`mapstructure:"username" validate:"required"`
	Password            string				`mapstructure:"password" validate:"required"`

	// optional value if the login realm is different than the target realm
	LoginRealm          string              `mapstructure:"realm"`
}

type RealmConfig struct {
	Name				string  			`mapstructure:"name" validate:"required"`
	Url					string				`mapstructure:"url" validate:"required"`
	ClientConfig        *AuthClientConfig   `mapstructure:"client" validate:"required_without=UserConfig"`
	UserConfig          *AuthUserConfig     `mapstructure:"user" validate:"required_without=ClientConfig""`
	SslVerify			bool				`mapstructure:"ssl-verify"`
	PreferredUsername   []string            `mapstructure:"preferred-username"`
	Groups              []string 			`mapstructure:"groups"`
	GroupPrefix			string				`mapstructure:"group-prefix"`
	GroupSuffix			string				`mapstructure:"group-suffix"`
	Aliases             map[string]string 	`mapstructure:"aliases"`
	Prune				bool				`mapstructure:"prune"`
}

type SyncConfig struct {
	Realms 				[]RealmConfig `mapstructure:"realms" validate:"dive"`
	Prune               bool          `mapstructure:"prune"`
}

func LoadConfig(path string) (SyncConfig, error) {
	// create default config with all the fields set to the defaults
	// where the default for that type is different
	config := SyncConfig{}

	viper.SetConfigFile(path)
	err := viper.ReadInConfig()
	if err != nil {
		return config, err
	}
	err = viper.Unmarshal(&config)

	// now validate configuration and return error if not valid
	validate := validator.New()
	err = validate.Struct(config)
	if err != nil {
		return config, err
	}

	return config, nil
}