## ADDED Requirements

### Requirement: YAML configuration file support
The system SHALL load configuration from YAML files using Viper.

#### Scenario: Load specified config file
- **WHEN** a config file path is explicitly provided
- **THEN** the system SHALL load configuration from that path

#### Scenario: Use default config path
- **WHEN** no config file path is provided
- **THEN** the system SHALL try loading from "configs/config.yaml"
- **AND** if that file does not exist, fall back to "configs/config.example.yaml"

---

### Requirement: Environment variable override
The system SHALL support overriding configuration via environment variables.

#### Scenario: Environment variable prefix
- **WHEN** loading configuration
- **THEN** environment variables with prefix "WECHAT_BOT_" SHALL be automatically mapped to corresponding config fields

#### Scenario: Empty environment variables allowed
- **WHEN** an environment variable is empty
- **THEN** the system SHALL accept it without error

---

### Requirement: Application configuration structure
The system SHALL define application-level configuration.

#### Scenario: App config fields
- **WHEN** accessing app configuration
- **THEN** the config SHALL contain name (application name)
- **AND** the config SHALL contain env (environment: dev, staging, prod)
- **AND** the config SHALL contain port (HTTP server port number)

---

### Requirement: WeChat configuration structure
The system SHALL define WeChat official account configuration.

#### Scenario: WeChat config fields
- **WHEN** accessing WeChat configuration
- **THEN** the config SHALL contain app_id (WeChat AppID)
- **AND** the config SHALL contain app_secret (WeChat AppSecret)
- **AND** the config SHALL contain token (verification token)
- **AND** the config SHALL contain encoding_aes_key (message encryption key)
- **AND** the config SHALL contain callback_url (callback endpoint URL)

---

### Requirement: LLM configuration structure
The system SHALL define LLM provider configuration supporting multiple providers.

#### Scenario: LLM providers map
- **WHEN** accessing LLM configuration
- **THEN** the config SHALL contain a map of provider configurations keyed by provider name

#### Scenario: Individual provider config
- **WHEN** accessing an individual provider config
- **THEN** the config SHALL contain api_key (API authentication key)
- **AND** the config SHALL contain model (model identifier)
- **AND** the config SHALL contain timeout (request timeout duration)

#### Scenario: Routing configuration
- **WHEN** accessing LLM routing configuration
- **THEN** the config SHALL contain default (default provider name)
- **AND** the config SHALL contain fallback_order (ordered list of fallback providers)

---

### Requirement: Logger configuration structure
The system SHALL define logger configuration.

#### Scenario: Logger config fields
- **WHEN** accessing logger configuration
- **THEN** the config SHALL contain level (log level: debug, info, warn, error)
- **AND** the config SHALL contain format (output format: json or console)
- **AND** the config SHALL contain output (output destination: stdout or file path)
