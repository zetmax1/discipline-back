package handlers

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"discipline-backend/internal/models"
	"discipline-backend/internal/repositories"
	"discipline-backend/internal/services"
)

// TaskHandler handles task HTTP requests.
type TaskHandler struct {
	taskService services.TaskService
}

// NewTaskHandler creates a new TaskHandler.
func NewTaskHandler(taskService services.TaskService) *TaskHandler {
	return &TaskHandler{taskService: taskService}
}

// --- Request types ---

// CreateTaskRequest is the expected JSON body for creating a task.
type CreateTaskRequest struct {
	Name      string `json:"name" binding:"required" example:"Morning routine"` // 1-120 chars
	Category  string `json:"category" binding:"required" example:"deep-work"`   // deep-work|study|admin|gym|rest
	StartTime string `json:"startTime" binding:"required" example:"09:00"`      // HH:MM
	EndTime   string `json:"endTime" binding:"required" example:"10:00"`        // HH:MM, after start, ≤ 24:00
	Energy    int    `json:"energy" binding:"required" example:"4"`             // 1-5
	Date      string `json:"date" binding:"required" example:"2026-03-02"`      // YYYY-MM-DD
}

// UpdateTaskRequest allows partial updates.
type UpdateTaskRequest struct {
	Name         *string `json:"name,omitempty" example:"Updated task name"`
	Category     *string `json:"category,omitempty" example:"study"`
	StartTime    *string `json:"startTime,omitempty" example:"09:30"`
	EndTime      *string `json:"endTime,omitempty" example:"11:00"`
	Status       *string `json:"status,omitempty" example:"completed"`
	Distractions *int    `json:"distractions,omitempty" example:"2"`
	Energy       *int    `json:"energy,omitempty" example:"3"`
	Date         *string `json:"date,omitempty" example:"2026-03-02"`
}

// BulkCreateTasksRequest is a list of 1–200 CreateTaskRequest items
type BulkCreateTasksRequest struct {
	Tasks []CreateTaskRequest `json:"tasks" binding:"required,min=1,max=200,dive"`
}

// TaskListResponse wraps a list of tasks.
type TaskListResponse struct {
	Tasks []interface{} `json:"tasks"`
	Count int           `json:"count"`
}

// respondTaskError maps validation errors to 400, overlaps to 409, a missing
// task to 404 and any other failure to 500.
func respondTaskError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, services.ErrInvalidTask):
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
	case errors.Is(err, repositories.ErrTaskOverlap):
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
	case errors.Is(err, gorm.ErrRecordNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": "Task not found"})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save task"})
	}
}

// --- Handlers ---

// CreateTask godoc
// @Summary      Create a new task
// @Description  Creates a time block for the authenticated user
// @Tags         Tasks
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        body  body  CreateTaskRequest  true  "Task data"
// @Success      201   {object}  map[string]interface{}
// @Failure      400   {object}  ErrorResponse
// @Failure      409   {object}  ErrorResponse
// @Router       /tasks [post]
func (h *TaskHandler) CreateTask(c *gin.Context) {
	userID := c.MustGet("userID").(uint)

	var req CreateTaskRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "Validation failed",
			"details": err.Error(),
		})
		return
	}

	task := &models.Task{
		UserID:    userID,
		Name:      req.Name,
		Category:  req.Category,
		StartTime: models.ClockTime(req.StartTime),
		EndTime:   models.ClockTime(req.EndTime),
		Energy:    req.Energy,
		Date:      models.Date(req.Date),
		Status:    models.StatusPending,
	}

	if err := h.taskService.CreateTask(task); err != nil {
		respondTaskError(c, err)
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"message": "Task created successfully",
		"task":    task,
	})
}

// BulkCreateTasks godoc
// @Summary      Create multiple tasks
// @Description  Creates multiple time blocks for the authenticated user
// @Tags         Tasks
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        body  body  BulkCreateTasksRequest  true  "Array of tasks"
// @Success      201   {object}  map[string]interface{}
// @Failure      400   {object}  ErrorResponse
// @Router       /tasks/bulk [post]
func (h *TaskHandler) BulkCreateTasks(c *gin.Context) {
	userID := c.MustGet("userID").(uint)

	var req BulkCreateTasksRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "Validation failed",
			"details": err.Error(),
		})
		return
	}

	var tasks []*models.Task
	for _, tr := range req.Tasks {
		tasks = append(tasks, &models.Task{
			UserID:    userID,
			Name:      tr.Name,
			Category:  tr.Category,
			StartTime: models.ClockTime(tr.StartTime),
			EndTime:   models.ClockTime(tr.EndTime),
			Energy:    tr.Energy,
			Date:      models.Date(tr.Date),
			Status:    models.StatusPending,
		})
	}

	if err := h.taskService.BulkCreateTasks(tasks); err != nil {
		respondTaskError(c, err)
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"message": "Tasks created successfully",
		"count":   len(tasks),
	})
}

// GetTasks godoc
// @Summary      List tasks
// @Description  Returns tasks for the authenticated user, optionally filtered by date
// @Tags         Tasks
// @Produce      json
// @Security     BearerAuth
// @Param        date  query  string  false  "Filter by date (YYYY-MM-DD)"  example(2026-03-02)
// @Success      200   {object}  map[string]interface{}
// @Failure      500   {object}  ErrorResponse
// @Router       /tasks [get]
func (h *TaskHandler) GetTasks(c *gin.Context) {
	userID := c.MustGet("userID").(uint)
	date := c.Query("date")

	// Default to today in the user's time zone
	if date == "" {
		date = userToday(c)
	}

	tasks, err := h.taskService.GetTasks(userID, date)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "Failed to fetch tasks",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"tasks": tasks,
		"count": len(tasks),
	})
}

// UpdateTask godoc
// @Summary      Update a task
// @Description  Partially updates a task (status, distractions, etc.)
// @Tags         Tasks
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        id    path  int                true  "Task ID"
// @Param        body  body  UpdateTaskRequest  true  "Fields to update"
// @Success      200   {object}  map[string]interface{}
// @Failure      400   {object}  ErrorResponse
// @Failure      404   {object}  ErrorResponse
// @Router       /tasks/{id} [put]
func (h *TaskHandler) UpdateTask(c *gin.Context) {
	userID := c.MustGet("userID").(uint)
	taskID, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid task ID"})
		return
	}

	var req UpdateTaskRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "Validation failed",
			"details": err.Error(),
		})
		return
	}

	updates := services.TaskUpdate{
		Name: req.Name, Category: req.Category, StartTime: req.StartTime, EndTime: req.EndTime,
		Status: req.Status, Date: req.Date, Distractions: req.Distractions, Energy: req.Energy,
	}

	task, err := h.taskService.UpdateTask(uint(taskID), userID, updates)
	if err != nil {
		respondTaskError(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "Task updated successfully",
		"task":    task,
	})
}

// SavePlanRequest is a generated weekly plan for the Monday-Sunday week starting at WeekStart.
type SavePlanRequest struct {
	WeekStart string              `json:"weekStart" binding:"required" example:"2026-11-02"` // a Monday
	Tasks     []CreateTaskRequest `json:"tasks" binding:"max=200,dive"`
}

// SavePlan godoc
// @Summary      Save a generated weekly plan
// @Description  Replaces the blocks previously generated for that week (manual blocks are kept) in one transaction.
// @Tags         Tasks
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        body  body  SavePlanRequest  true  "Week and blocks"
// @Success      200   {object}  map[string]interface{}
// @Failure      400   {object}  ErrorResponse
// @Failure      409   {object}  ErrorResponse
// @Router       /tasks/plan [put]
func (h *TaskHandler) SavePlan(c *gin.Context) {
	userID := c.MustGet("userID").(uint)
	var req SavePlanRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Validation failed", "details": err.Error()})
		return
	}
	tasks := make([]*models.Task, 0, len(req.Tasks))
	for _, tr := range req.Tasks {
		tasks = append(tasks, &models.Task{
			Name:      tr.Name,
			Category:  tr.Category,
			StartTime: models.ClockTime(tr.StartTime),
			EndTime:   models.ClockTime(tr.EndTime),
			Energy:    tr.Energy,
			Date:      models.Date(tr.Date),
			Status:    models.StatusPending,
		})
	}
	planID, replaced, err := h.taskService.SaveWeekPlan(userID, req.WeekStart, tasks)
	if err != nil {
		respondTaskError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"planId": planID, "count": len(tasks), "replaced": replaced})
}

// AdjustDistractionsRequest changes a block's distraction count by one.
type AdjustDistractionsRequest struct {
	Delta *int `json:"delta" binding:"required" example:"1"` // 1 or -1
}

// AdjustDistractions godoc
// @Summary      Increment or decrement distractions
// @Description  Atomically adds delta (1 or -1) to a block's distraction count, clamped to 0-100
// @Tags         Tasks
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        id    path  int                        true  "Task ID"
// @Param        body  body  AdjustDistractionsRequest  true  "Delta"
// @Success      200   {object}  map[string]interface{}
// @Failure      400   {object}  ErrorResponse
// @Failure      404   {object}  ErrorResponse
// @Router       /tasks/{id}/distractions [post]
func (h *TaskHandler) AdjustDistractions(c *gin.Context) {
	userID := c.MustGet("userID").(uint)
	taskID, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid task ID"})
		return
	}
	var req AdjustDistractionsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Validation failed", "details": err.Error()})
		return
	}
	task, err := h.taskService.AdjustDistractions(uint(taskID), userID, *req.Delta)
	if err != nil {
		respondTaskError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"task": task})
}

// DeleteTask godoc
// @Summary      Delete a task
// @Description  Deletes a task by ID
// @Tags         Tasks
// @Produce      json
// @Security     BearerAuth
// @Param        id  path  int  true  "Task ID"
// @Success      200  {object}  map[string]interface{}
// @Failure      404  {object}  ErrorResponse
// @Router       /tasks/{id} [delete]
func (h *TaskHandler) DeleteTask(c *gin.Context) {
	userID := c.MustGet("userID").(uint)
	taskID, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid task ID"})
		return
	}

	if err := h.taskService.DeleteTask(uint(taskID), userID); err != nil {
		respondTaskError(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Task deleted successfully"})
}
