## MODIFIED Requirements

### Requirement: Provider factory

The system SHALL create LLM provider instances based on configuration.

#### Scenario: Create OpenAI provider
- **WHEN** provider name is "openai"
- **THEN** the system SHALL create an OpenAIProvider instance

#### Scenario: Create Azure provider
- **WHEN** provider name is "azure"
- **THEN** the system SHALL create an OpenAIProvider instance configured for Azure OpenAI Service
