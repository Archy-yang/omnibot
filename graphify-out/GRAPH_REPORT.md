# Graph Report - /Users/yangqi/workspace/self_project/omnibot  (2026-05-10)

## Corpus Check
- 31 files · ~38,203 words
- Verdict: corpus is large enough that graph structure adds value.

## Summary
- 239 nodes · 476 edges · 21 communities detected
- Extraction: 56% EXTRACTED · 44% INFERRED · 0% AMBIGUOUS · INFERRED: 210 edges (avg confidence: 0.8)
- Token cost: 0 input · 0 output

## Community Hubs (Navigation)
- [[_COMMUNITY_Community 0|Community 0]]
- [[_COMMUNITY_Community 1|Community 1]]
- [[_COMMUNITY_Community 2|Community 2]]
- [[_COMMUNITY_Community 3|Community 3]]
- [[_COMMUNITY_Community 4|Community 4]]
- [[_COMMUNITY_Community 5|Community 5]]
- [[_COMMUNITY_Community 6|Community 6]]
- [[_COMMUNITY_Community 7|Community 7]]
- [[_COMMUNITY_Community 8|Community 8]]
- [[_COMMUNITY_Community 9|Community 9]]
- [[_COMMUNITY_Community 10|Community 10]]
- [[_COMMUNITY_Community 11|Community 11]]
- [[_COMMUNITY_Community 12|Community 12]]
- [[_COMMUNITY_Community 13|Community 13]]
- [[_COMMUNITY_Community 14|Community 14]]
- [[_COMMUNITY_Community 15|Community 15]]
- [[_COMMUNITY_Community 16|Community 16]]
- [[_COMMUNITY_Community 17|Community 17]]
- [[_COMMUNITY_Community 18|Community 18]]
- [[_COMMUNITY_Community 19|Community 19]]
- [[_COMMUNITY_Community 20|Community 20]]

## God Nodes (most connected - your core abstractions)
1. `Handler` - 19 edges
2. `init()` - 19 edges
3. `Error()` - 19 edges
4. `NewUser()` - 18 edges
5. `InitDB()` - 13 edges
6. `SetupRouter()` - 13 edges
7. `InfoWithFields()` - 12 edges
8. `NewHandler()` - 11 edges
9. `WarnWithFields()` - 11 edges
10. `TestWechatAccountRepository_GetByUnionID()` - 8 edges

## Surprising Connections (you probably didn't know these)
- `SetupRouter()` --calls--> `CORS()`  [INFERRED]
  /Users/yangqi/workspace/self_project/omnibot/internal/api/routes.go → internal/middleware/middleware.go
- `Logger()` --calls--> `Info()`  [INFERRED]
  internal/middleware/middleware.go → /Users/yangqi/workspace/self_project/omnibot/pkg/logger/logger.go
- `SetupRouter()` --calls--> `Logger()`  [INFERRED]
  /Users/yangqi/workspace/self_project/omnibot/internal/api/routes.go → internal/middleware/middleware.go
- `Recovery()` --calls--> `Error()`  [INFERRED]
  internal/middleware/middleware.go → /Users/yangqi/workspace/self_project/omnibot/pkg/logger/logger.go
- `SetupRouter()` --calls--> `Recovery()`  [INFERRED]
  /Users/yangqi/workspace/self_project/omnibot/internal/api/routes.go → internal/middleware/middleware.go

## Hyperedges (group relationships)
- **Server Startup Flow** — cmdserver_main_main, pkgconfig_config_load, pkglogger_logger_init, internalapi_routes_setuprouter [EXTRACTED 1.00]
- **Core Business Modules** — systemdesign_wechat_module, systemdesign_chat_module, systemdesign_memory_module [EXTRACTED 1.00]
- **Configuration Management System** — pkgconfig_config_config, pkgconfig_config_load, configsexample_configexample, configsprod_configprod [EXTRACTED 0.95]

## Communities

### Community 0 - "Community 0"
Cohesion: 0.09
Nodes (27): GormUserRepository, GormWechatAccountRepository, NewUser(), NewUserRepository(), setupTestDB(), TestUserRepository_Create(), TestUserRepository_GetByID(), TestUserRepository_GetByPhone() (+19 more)

### Community 1 - "Community 1"
Cohesion: 0.19
Nodes (10): Debug(), DebugWithFields(), ErrorWithFields(), Fatal(), FatalWithFields(), Info(), InfoWithFields(), Warn() (+2 more)

### Community 2 - "Community 2"
Cohesion: 0.14
Nodes (23): TestMain(), TestClient_ChatCompletion_AllFailed(), TestClient_ChatCompletion_Fallback(), TestFactory_CreateClient_Success(), NewHandler(), TestHandler_HandleMessage_ImageMessage(), TestHandler_HandleMessage_SubscribeEvent_CreatesUser(), TestHandler_HandleMessage_SubscribeEvent_UserServiceError() (+15 more)

### Community 3 - "Community 3"
Cohesion: 0.21
Nodes (13): autoMigrate(), InitDB(), TestClose(), TestGetGormDB(), TestHealthCheck(), TestInitDB_PostgreSQL_DriverLoads(), TestInitDB_SQLite(), TestStats() (+5 more)

### Community 4 - "Community 4"
Cohesion: 0.12
Nodes (14): AppConfig, Config, DatabaseConfig, ExtractionConfig, LLMConfig, LMRoutingConfig, LoggerConfig, MemoryConfig (+6 more)

### Community 5 - "Community 5"
Cohesion: 0.21
Nodes (11): NewDoubaoProvider(), createProvider(), NewClient(), TestFactory_CreateClient_NotFoundDefault(), Client, Error(), CORS(), Logger() (+3 more)

### Community 6 - "Community 6"
Cohesion: 0.24
Nodes (4): TestLLMConfig_GetBaseURL(), TestLLMConfig_GetModel(), TestLLMConfig_IsEnabled(), LLMConfig

### Community 7 - "Community 7"
Cohesion: 0.2
Nodes (8): qwenInput, qwenMessage, qwenOutput, QwenProvider, qwenRequest, qwenResponse, qwenSettings, qwenUsage

### Community 8 - "Community 8"
Cohesion: 0.22
Nodes (7): doubaoChoice, doubaoError, doubaoMessage, DoubaoProvider, doubaoRequest, doubaoResponse, doubaoUsage

### Community 9 - "Community 9"
Cohesion: 0.25
Nodes (6): openAIChoice, openAIError, openAIMessage, OpenAIProvider, openAIRequest, openAIResponse

### Community 10 - "Community 10"
Cohesion: 0.42
Nodes (7): Decrypt(), Encrypt(), getEncryptKey(), TestAES_DifferentNonce(), TestAES_EncryptDecrypt(), TestAES_InvalidKeyLength(), TestAES_WrongKey()

### Community 11 - "Community 11"
Cohesion: 0.39
Nodes (8): Project README Documentation, System Design Document, Chat Service Module Design, Memory Service Module Design, Message Processing Flow, Overall System Architecture, System Characteristics Rationale, WeChat Service Module Design

### Community 12 - "Community 12"
Cohesion: 0.33
Nodes (5): parseMessage(), Config, LLMClient, Message, UserService

### Community 13 - "Community 13"
Cohesion: 0.33
Nodes (1): Handler

### Community 14 - "Community 14"
Cohesion: 0.47
Nodes (2): TestUser_StatusFlow(), User

### Community 15 - "Community 15"
Cohesion: 0.67
Nodes (2): ChatMessage, LLMProvider

### Community 16 - "Community 16"
Cohesion: 1.0
Nodes (1): Main Server Entry Point Function

### Community 17 - "Community 17"
Cohesion: 1.0
Nodes (1): Project Development Guidance

### Community 18 - "Community 18"
Cohesion: 1.0
Nodes (1): Example Configuration File

### Community 19 - "Community 19"
Cohesion: 1.0
Nodes (1): Production Configuration File

### Community 20 - "Community 20"
Cohesion: 1.0
Nodes (1): Go Module Dependencies

## Knowledge Gaps
- **46 isolated node(s):** `Main Server Entry Point Function`, `Option`, `Message`, `LLMClient`, `UserService` (+41 more)
  These have ≤1 connection - possible missing edges or undocumented components.
- **Thin community `Community 16`** (1 nodes): `Main Server Entry Point Function`
  Too small to be a meaningful cluster - may be noise or needs more connections extracted.
- **Thin community `Community 17`** (1 nodes): `Project Development Guidance`
  Too small to be a meaningful cluster - may be noise or needs more connections extracted.
- **Thin community `Community 18`** (1 nodes): `Example Configuration File`
  Too small to be a meaningful cluster - may be noise or needs more connections extracted.
- **Thin community `Community 19`** (1 nodes): `Production Configuration File`
  Too small to be a meaningful cluster - may be noise or needs more connections extracted.
- **Thin community `Community 20`** (1 nodes): `Go Module Dependencies`
  Too small to be a meaningful cluster - may be noise or needs more connections extracted.

## Suggested Questions
_Questions this graph is uniquely positioned to answer:_

- **Why does `Error()` connect `Community 5` to `Community 1`, `Community 2`, `Community 3`, `Community 9`, `Community 10`?**
  _High betweenness centrality (0.126) - this node is a cross-community bridge._
- **Why does `SetupRouter()` connect `Community 5` to `Community 0`, `Community 1`, `Community 2`, `Community 3`?**
  _High betweenness centrality (0.112) - this node is a cross-community bridge._
- **Why does `NewUser()` connect `Community 0` to `Community 2`, `Community 14`?**
  _High betweenness centrality (0.110) - this node is a cross-community bridge._
- **Are the 18 inferred relationships involving `init()` (e.g. with `TestMain()` and `TestHandler_Verify_ValidSignature()`) actually correct?**
  _`init()` has 18 INFERRED edges - model-reasoned connections that need verification._
- **Are the 17 inferred relationships involving `Error()` (e.g. with `Recovery()` and `.Stats()`) actually correct?**
  _`Error()` has 17 INFERRED edges - model-reasoned connections that need verification._
- **Are the 17 inferred relationships involving `NewUser()` (e.g. with `TestWechatAccountRepository_Create()` and `TestWechatAccountRepository_GetByOpenID()`) actually correct?**
  _`NewUser()` has 17 INFERRED edges - model-reasoned connections that need verification._
- **Are the 10 inferred relationships involving `InitDB()` (e.g. with `InfoWithFields()` and `TestInitDB_SQLite()`) actually correct?**
  _`InitDB()` has 10 INFERRED edges - model-reasoned connections that need verification._