## ADDED Requirements

### Requirement: WeChat server verification endpoint
The system SHALL expose a GET /wechat/callback endpoint for WeChat server signature validation.

#### Scenario: Valid signature verification
- **WHEN** WeChat server sends a GET request with valid signature, timestamp, nonce, and echostr parameters
- **THEN** the system responds with HTTP 200 and returns the echostr value

#### Scenario: Missing required parameters
- **WHEN** any of signature, timestamp, nonce, or echostr parameters are missing
- **THEN** the system responds with HTTP 400 and "Invalid parameters" message

#### Scenario: Invalid signature
- **WHEN** the computed SHA1 hash does not match the provided signature
- **THEN** the system responds with HTTP 403 and "Invalid signature" message

---

### Requirement: WeChat message receiving endpoint
The system SHALL expose a POST /wechat/callback endpoint for receiving WeChat messages.

#### Scenario: Receive valid XML message
- **WHEN** WeChat server sends a POST request with valid XML message body
- **THEN** the system parses the XML and returns HTTP 200 with XML response

#### Scenario: Failed to read request body
- **WHEN** the request body cannot be read
- **THEN** the system responds with HTTP 500 and "Failed to read request body" message

#### Scenario: Failed to parse XML
- **WHEN** the XML body is malformed and cannot be parsed
- **THEN** the system responds with HTTP 400 and "Failed to parse message" message

---

### Requirement: Text message handling
The system SHALL process incoming text messages from WeChat users by calling the LLM client to generate an intelligent response.

#### Scenario: Text message received and LLM succeeds
- **WHEN** a user sends a text message (MsgType = "text")
- **AND** the LLM client successfully generates a response
- **THEN** the system SHALL return the LLM-generated response as the message content

#### Scenario: Text message received and LLM fails
- **WHEN** a user sends a text message (MsgType = "text")
- **AND** all LLM providers fail to generate a response
- **THEN** the system SHALL return "服务暂时不可用，请稍后再试"

---

### Requirement: Image message handling
The system SHALL process incoming image messages from WeChat users by calling the LLM client with a type indicator prompt.

#### Scenario: Image message received and LLM succeeds
- **WHEN** a user sends an image message (MsgType = "image")
- **AND** the LLM client successfully generates a response
- **THEN** the system SHALL return the LLM-generated response based on prompt "用户发送了一张图片"

---

### Requirement: Voice message handling
The system SHALL process incoming voice messages from WeChat users by calling the LLM client with a type indicator prompt.

#### Scenario: Voice message received and LLM succeeds
- **WHEN** a user sends a voice message (MsgType = "voice")
- **AND** the LLM client successfully generates a response
- **THEN** the system SHALL return the LLM-generated response based on prompt "用户发送了一条语音消息"

---

### Requirement: Subscribe event handling
The system SHALL process user subscription events by calling the LLM client to generate a welcome message.

#### Scenario: User subscribes and LLM succeeds
- **WHEN** a user subscribes to the official account (Event = "subscribe")
- **AND** the LLM client successfully generates a response
- **THEN** the system SHALL return the LLM-generated welcome message based on prompt "用户刚刚关注了公众号，请生成友好的欢迎语"

#### Scenario: User unsubscribes
- **WHEN** a user unsubscribes from the official account (Event = "unsubscribe")
- **THEN** the system logs the event and returns an empty response (no message sent)

---

### Requirement: XML response format
The system SHALL format all responses as valid XML with proper CDATA sections.

#### Scenario: Response format validation
- **WHEN** generating any message response
- **THEN** the XML SHALL contain ToUserName, FromUserName, CreateTime, MsgType, and Content fields
- **AND** all string fields SHALL be wrapped in `<![CDATA[...]]>` sections
- **AND** MsgType SHALL be "text"

---

### Requirement: LLM client dependency injection
The WeChat handler SHALL receive an initialized LLM client via constructor injection.

#### Scenario: Handler creation with LLM client
- **WHEN** creating a new WeChat handler instance
- **THEN** the NewHandler function SHALL accept an *llm.Client parameter
- **AND** the handler SHALL store the client for message processing

#### Scenario: Route setup injects LLM client
- **WHEN** setting up the Gin router
- **THEN** the SetupRouter function SHALL create an LLM client from configuration
- **AND** inject it into the WeChat handler

---

### Requirement: System prompt configuration
The system SHALL prepend a system prompt to all LLM conversations.

#### Scenario: System prompt included in all LLM calls
- **WHEN** calling the LLM client for any message type
- **THEN** the first message in the conversation SHALL be a system role message with content "你是一个友好的智能客服助手，请用简洁的中文回应用户的问题。"
