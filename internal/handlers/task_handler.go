package handlers

import (
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"discipline-backend/internal/models"
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
	Name      string `json:"name" binding:"required,min=1,max=200" example:"Morning routine"`
	Category  string `json:"category" binding:"required,oneof=deep-work study admin gym rest" example:"deep-work"`
	StartTime string `json:"startTime" binding:"required" example:"09:00"`
	EndTime   string `json:"endTime" binding:"required" example:"10:00"`
	Energy    int    `json:"energy" binding:"required,min=1,max=5" example:"4"`
	Date      string `json:"date" binding:"required" example:"2026-03-02"`
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

// TaskListResponse wraps a list of tasks.
type TaskListResponse struct {
	Tasks []interface{} `json:"tasks"`
	Count int           `json:"count"`
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
		StartTime: req.StartTime,
		EndTime:   req.EndTime,
		Energy:    req.Energy,
		Date:      req.Date,
		Status:    models.StatusPending,
	}

	if err := h.taskService.CreateTask(task); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "Failed to create task",
		})
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"message": "Task created successfully",
		"task":    task,
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

	// Default to today if no date provided
	if date == "" {
		date = time.Now().Format("2006-01-02")
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

	// Build updates map from non-nil fields
	updates := make(map[string]interface{})
	if req.Name != nil {
		updates["name"] = *req.Name
	}
	if req.Category != nil {
		updates["category"] = *req.Category
	}
	if req.StartTime != nil {
		updates["startTime"] = *req.StartTime
	}
	if req.EndTime != nil {
		updates["endTime"] = *req.EndTime
	}
	if req.Status != nil {
		updates["status"] = *req.Status
	}
	if req.Distractions != nil {
		updates["distractions"] = float64(*req.Distractions)
	}
	if req.Energy != nil {
		updates["energy"] = float64(*req.Energy)
	}
	if req.Date != nil {
		updates["date"] = *req.Date
	}

	task, err := h.taskService.UpdateTask(uint(taskID), userID, updates)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Task not found"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "Task updated successfully",
		"task":    task,
	})
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
		c.JSON(http.StatusNotFound, gin.H{"error": "Task not found"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Task deleted successfully"})
}

// GetDashboardStats godoc
// @Summary      Get dashboard statistics
// @Description  Returns computed stats for a given date (completion, focus score, hours)
// @Tags         Dashboard
// @Produce      json
// @Security     BearerAuth
// @Param        date  query  string  false  "Date (YYYY-MM-DD), defaults to today"  example(2026-03-02)
// @Success      200   {object}  services.DashboardStats
// @Failure      500   {object}  ErrorResponse
// @Router       /dashboard/stats [get]
func (h *TaskHandler) GetDashboardStats(c *gin.Context) {
	userID := c.MustGet("userID").(uint)
	date := c.Query("date")

	if date == "" {
		date = time.Now().Format("2006-01-02")
	}

	stats, err := h.taskService.GetDashboardStats(userID, date)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to compute stats"})
		return
	}

	c.JSON(http.StatusOK, stats)
}
