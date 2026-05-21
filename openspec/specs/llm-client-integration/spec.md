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

### Requirement: OpenAI API Provider
The system SHALL support OpenAI-compatible APIs as an LLM provider, including OpenAI official, Azure OpenAI, and any compatible third-party services.

#### Scenario: OpenAI API Request Format
- **WHEN** calling ChatCompletion
- **THEN** the request SHALL POST to `{base_url}/chat/completions`
- **AND** the request body SHALL contain `model` and `messages` fields
- **AND** messages SHALL follow OpenAI format with `role` and `content`

#### Scenario: OpenAI API Success Response Parsing
- **WHEN** receiving a successful response
- **THEN** the system SHALL extract the generated text from `choices[0].message.content`

#### Scenario: OpenAI API Error Response Handling
- **WHEN** receiving an error response from OpenAI API
- **THEN** the system SHALL extract the error message from `error.message`
- **AND** return it as a Go error

#### Scenario: Bearer Token Authentication
- **WHEN** making API requests
- **THEN** the system SHALL include `Authorization: Bearer {apiKey}` header
- **AND** SHALL include `Content-Type: application/json` header

---

### Requirement: OpenAI Provider HTTP Client
The OpenAI provider SHALL use a configured HTTP client with timeout.

#### Scenario: Configured Timeout Applied
- **WHEN** creating the OpenAI provider with a timeout duration
- **THEN** the internal http.Client SHALL use that timeout for all requests

#### Scenario: Request Context Propagation
- **WHEN** calling ChatCompletion with a context
- **THEN** the context SHALL be propagated to the HTTP request
- **AND** request cancellation SHALL be respected

---

### Requirement: Provider factory
The system SHALL create LLM provider instances based on configuration.

#### Scenario: Create Qwen provider
- **WHEN** provider name is "qwen", "tongyi", or "alibabacloud"
- **THEN** the system SHALL create a Qwen provider instance

#### Scenario: Create Doubao provider
- **WHEN** provider name is "doubao", "byteDance", or "volcengine"
- **THEN** the system SHALL create a Doubao provider instance

#### Scenario: Create OpenAI provider
- **WHEN** provider name is "openai"
- **THEN** the system SHALL create an OpenAIProvider instance
- **AND** the provider SHALL be configured with api_key, base_url, model, and timeout

#### Scenario: Create Azure OpenAI provider
- **WHEN** provider name is "azure"
- **THEN** the system SHALL create an OpenAIProvider instance
- **AND** use the configured base_url for Azure API endpoint

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

---

### Requirement: WeChat handler integration
The LLM client SHALL be usable by the WeChat message handler for generating intelligent responses.

#### Scenario: WeChat handler calls LLM client with system prompt and user message
- **WHEN** the WeChat handler processes a user message
- **THEN** it SHALL construct a message array containing:
  1. A system role message with the configured system prompt
  2. A user role message with the user's input or type indicator
- **AND** call the ChatCompletion method with the constructed message array

#### Scenario: WeChat handler uses request context for LLM call
- **WHEN** the WeChat handler calls the LLM client
- **THEN** it SHALL pass the Gin request context as the first parameter
- **AND** respect any cancellation or deadlines from the context
