## ADDED Requirements

### Requirement: OpenAI API Provider

The system SHALL support OpenAI-compatible APIs as an LLM provider, including OpenAI official, Azure OpenAI, and any compatible third-party services.

#### Scenario: OpenAI Provider Creation
- **WHEN** creating a provider with name "openai"
- **THEN** the factory SHALL return an OpenAIProvider instance
- **AND** the provider SHALL be configured with api_key, base_url, model, and timeout

#### Scenario: Azure OpenAI Provider Creation
- **WHEN** creating a provider with name "azure"
- **THEN** the factory SHALL return an OpenAIProvider instance
- **AND** use the configured base_url for Azure API endpoint

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
