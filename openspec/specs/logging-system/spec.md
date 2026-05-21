## ADDED Requirements

### Requirement: Zap logger integration
The system SHALL use Uber's Zap library for structured logging.

#### Scenario: Logger initialization
- **WHEN** initializing the logger with configuration
- **THEN** the system SHALL create a Zap logger with caller information
- **AND** the system SHALL enable stack traces for error level logs
- **AND** the global Zap logger SHALL be replaced with the configured instance

---

### Requirement: Log level configuration
The system SHALL support configurable log levels.

#### Scenario: Valid log level
- **WHEN** the configured log level is valid (debug, info, warn, error)
- **THEN** the system SHALL apply that level to the logger

#### Scenario: Invalid log level
- **WHEN** the configured log level is invalid or cannot be parsed
- **THEN** the system SHALL default to info level

---

### Requirement: Log output format configuration
The system SHALL support configurable log output formats.

#### Scenario: JSON format output
- **WHEN** format is set to "json"
- **THEN** the system SHALL output logs in JSON format with ISO8601 timestamps

#### Scenario: Console format output
- **WHEN** format is set to "console"
- **THEN** the system SHALL output logs in human-readable console format

#### Scenario: Default format
- **WHEN** format is not specified or unrecognized
- **THEN** the system SHALL default to JSON format

---

### Requirement: Log output destination configuration
The system SHALL support configurable log output destinations.

#### Scenario: Stdout output
- **WHEN** output is set to "stdout"
- **THEN** the system SHALL write logs to standard output

#### Scenario: File output with rotation
- **WHEN** output is set to a file path
- **THEN** the system SHALL use Lumberjack for log rotation
- **AND** maximum file size SHALL be 100MB
- **AND** SHALL retain up to 7 backup files
- **AND** SHALL retain logs for up to 28 days
- **AND** SHALL compress rotated log files

---

### Requirement: Logging API methods
The system SHALL provide both simple and structured logging methods.

#### Scenario: Simple logging methods
- **WHEN** logging without additional context fields
- **THEN** the system SHALL provide Debug(), Info(), Warn(), Error(), and Fatal() methods

#### Scenario: Structured logging with fields
- **WHEN** logging with additional context fields
- **THEN** the system SHALL provide DebugWithFields(), InfoWithFields(), WarnWithFields(), ErrorWithFields(), and FatalWithFields() methods
- **AND** each method SHALL accept zap.Field parameters for structured data
