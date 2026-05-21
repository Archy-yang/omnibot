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
The system SHALL process incoming text messages from WeChat users.

#### Scenario: Text message received
- **WHEN** a user sends a text message (MsgType = "text")
- **THEN** the system extracts the Content field and returns a response message

---

### Requirement: Image message handling
The system SHALL process incoming image messages from WeChat users.

#### Scenario: Image message received
- **WHEN** a user sends an image message (MsgType = "image")
- **THEN** the system extracts PicUrl and MediaId fields and returns a response message

---

### Requirement: Voice message handling
The system SHALL process incoming voice messages from WeChat users.

#### Scenario: Voice message received
- **WHEN** a user sends a voice message (MsgType = "voice")
- **THEN** the system extracts MediaId and Format fields and returns a response message

---

### Requirement: Subscribe event handling
The system SHALL process user subscription events.

#### Scenario: User subscribes
- **WHEN** a user subscribes to the official account (Event = "subscribe")
- **THEN** the system returns a welcome response message

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
