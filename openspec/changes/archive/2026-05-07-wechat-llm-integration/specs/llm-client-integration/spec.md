## ADDED Requirements

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
