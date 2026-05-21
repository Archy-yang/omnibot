## ADDED Requirements

### Requirement: Unified LLM provider interface
The system SHALL define a unified interface for all LLM providers to ensure interchangeability.

#### Scenario: Interface contract
- **WHEN** implementing a new LLM provider
- **THEN** the provider SHALL implement the ChatCompletion method with signature:
  `ChatCompletion(ctx context.Context, messages []ChatMessage) (string, error)`

#### Scenario: ChatMessage structure
- **WHEN** constructing messages for LLM completion
- **THEN** each message SHALL contain a Role field ("system", "user", or "assistant")
- **AND** each message SHALL contain a Content field with the message text

---

### Requirement: Tongyi Qwen provider
The system SHALL support Alibaba Cloud Tongyi Qwen as an LLM provider.

#### Scenario: Qwen API request format
- **WHEN** calling the Qwen API
- **THEN** the request SHALL include model and input.messages fields
- **AND** messages SHALL conform to Qwen API format

#### Scenario: Qwen API response parsing
- **WHEN** receiving a successful response from Qwen API
- **THEN** the system SHALL extract the generated text from choices[0].message.content

#### Scenario: Qwen API error handling
- **WHEN** Qwen API returns an error
- **THEN** the system SHALL return an error containing the error message

---

### Requirement: Doubao provider
The system SHALL support ByteDance Doubao as an LLM provider.

#### Scenario: Doubao API request format
- **WHEN** calling the Doubao API
- **THEN** the request SHALL include model and messages fields
- **AND** messages SHALL conform to OpenAI-compatible format

#### Scenario: Doubao API response parsing
- **WHEN** receiving a successful response from Doubao API
- **THEN** the system SHALL extract the generated text from choices[0].message.content

#### Scenario: Doubao API error handling
- **WHEN** Doubao API returns an error
- **THEN** the system SHALL return an error containing the error message

---

### Requirement: Provider factory
The system SHALL create LLM provider instances based on configuration.

#### Scenario: Create Qwen provider
- **WHEN** provider name is "qwen", "tongyi", or "alibabacloud"
- **THEN** the system SHALL create a Qwen provider instance

#### Scenario: Create Doubao provider
- **WHEN** provider name is "doubao", "byteDance", or "volcengine"
- **THEN** the system SHALL create a Doubao provider instance

#### Scenario: Unknown provider type
- **WHEN** provider name does not match any known type
- **THEN** the system SHALL return an error: "unknown provider type: <name>"

---

### Requirement: Automatic fallback mechanism
The system SHALL automatically try fallback providers when the default provider fails.

#### Scenario: Default provider succeeds
- **WHEN** the default provider successfully completes the request
- **THEN** the system returns the response immediately without trying fallbacks

#### Scenario: Default provider fails, fallback succeeds
- **WHEN** the default provider fails
- **THEN** the system SHALL try each fallback provider in configured order
- **AND** return the first successful response

#### Scenario: All providers fail
- **WHEN** all configured providers fail
- **THEN** the system SHALL return an error: "all providers failed"

#### Scenario: Provider creation failure logged
- **WHEN** a provider fails to initialize during client creation
- **THEN** the system SHALL log a warning and continue with available providers

---

### Requirement: Per-provider timeout configuration
The system SHALL support independent timeout configuration for each LLM provider.

#### Scenario: Parse valid timeout duration
- **WHEN** a provider configuration includes a valid timeout string (e.g., "30s")
- **THEN** the system SHALL parse and apply that timeout for that provider

#### Scenario: Default timeout when unspecified
- **WHEN** a provider configuration does not specify a timeout
- **THEN** the system SHALL use a default timeout of 30 seconds
