package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	domainagent "omnibot/internal/domain/agent"
	"omnibot/internal/middleware"
	agentpkg "omnibot/internal/service/agent"
)

// 34 号:Web 任务中心"继续"按钮 → POST /agent/tasks/:id/resume(仅 paused 计划)。

type fakePlanResumer struct {
	called bool
	err    error
}

func (f *fakePlanResumer) Resume(_ context.Context, _, _ int64) error {
	f.called = true
	return f.err
}

func TestResumeTask_OK(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler, repo := setupAgentTaskHandlerTest(t)
	resumer := &fakePlanResumer{}
	handler.SetPlanRunner(resumer)

	plan := domainagent.NewPlanTask(42, domainagent.NewTaskSpec("做攻略"), domainagent.SourceWeb, "", 0)
	plan.Name = "成都三日游"
	require.NoError(t, repo.Create(plan))
	mustTransitionWeb(t, repo, plan.ID, domainagent.TaskStatusPending, domainagent.TaskStatusRunning, nil, nil)
	mustTransitionWeb(t, repo, plan.ID, domainagent.TaskStatusRunning, domainagent.TaskStatusPaused, nil, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)
	c.Params = gin.Params{{Key: "id", Value: strconv.FormatInt(plan.ID, 10)}}
	c.Set(middleware.AuthUserIDKey, int64(42))
	handler.HandleResumeTask(c)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.True(t, resumer.called)
}

func TestResumeTask_ConflictWhenNotPaused(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler, repo := setupAgentTaskHandlerTest(t)
	handler.SetPlanRunner(&fakePlanResumer{})

	plan := domainagent.NewPlanTask(42, domainagent.NewTaskSpec("g"), domainagent.SourceWeb, "", 0)
	require.NoError(t, repo.Create(plan))
	mustTransitionWeb(t, repo, plan.ID, domainagent.TaskStatusPending, domainagent.TaskStatusRunning, nil, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)
	c.Params = gin.Params{{Key: "id", Value: strconv.FormatInt(plan.ID, 10)}}
	c.Set(middleware.AuthUserIDKey, int64(42))
	handler.HandleResumeTask(c)
	assert.Equal(t, http.StatusConflict, w.Code)
}

func TestResumeTask_NotOwner(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler, repo := setupAgentTaskHandlerTest(t)
	handler.SetPlanRunner(&fakePlanResumer{})

	plan := domainagent.NewPlanTask(42, domainagent.NewTaskSpec("g"), domainagent.SourceWeb, "", 0)
	require.NoError(t, repo.Create(plan))

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)
	c.Params = gin.Params{{Key: "id", Value: strconv.FormatInt(plan.ID, 10)}}
	c.Set(middleware.AuthUserIDKey, int64(99))
	handler.HandleResumeTask(c)
	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestResumeTask_NotAPlan(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler, repo := setupAgentTaskHandlerTest(t)
	handler.SetPlanRunner(&fakePlanResumer{})

	task := domainagent.NewAgentTask(42, domainagent.NewTaskSpec("普通任务"), domainagent.SourceWeb, "")
	require.NoError(t, repo.Create(task))

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)
	c.Params = gin.Params{{Key: "id", Value: strconv.FormatInt(task.ID, 10)}}
	c.Set(middleware.AuthUserIDKey, int64(42))
	handler.HandleResumeTask(c)
	assert.Equal(t, http.StatusConflict, w.Code)
}

func TestResumeTask_ResumerError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler, repo := setupAgentTaskHandlerTest(t)
	handler.SetPlanRunner(&fakePlanResumer{err: agentpkg.ErrPlanNotResumable})

	plan := domainagent.NewPlanTask(42, domainagent.NewTaskSpec("g"), domainagent.SourceWeb, "", 0)
	require.NoError(t, repo.Create(plan))
	mustTransitionWeb(t, repo, plan.ID, domainagent.TaskStatusPending, domainagent.TaskStatusRunning, nil, nil)
	mustTransitionWeb(t, repo, plan.ID, domainagent.TaskStatusRunning, domainagent.TaskStatusPaused, nil, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)
	c.Params = gin.Params{{Key: "id", Value: strconv.FormatInt(plan.ID, 10)}}
	c.Set(middleware.AuthUserIDKey, int64(42))
	handler.HandleResumeTask(c)
	assert.Equal(t, http.StatusConflict, w.Code)
}
