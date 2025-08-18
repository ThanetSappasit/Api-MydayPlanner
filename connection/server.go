package connection

import (
	"log"
	"mydayplanner/controller"
	"mydayplanner/controller/admin"
	"mydayplanner/controller/assigned"
	"mydayplanner/controller/attachments"
	"mydayplanner/controller/auth"
	"mydayplanner/controller/board"
	"mydayplanner/controller/checklist"
	"mydayplanner/controller/notification"
	"mydayplanner/controller/report"
	"mydayplanner/controller/shareboard"
	"mydayplanner/controller/task"
	"mydayplanner/controller/user"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
)

func StartServer() {
	router := gin.Default()

	DB, err := DBConnection()
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	FB, err := FBConnection()
	if err != nil {
		log.Fatalf("Failed to initialize Firestore client: %v", err)
	}

	router.GET("/", func(c *gin.Context) {
		c.JSON(200, gin.H{"message": "Api is running!"})
	})

	router.Use(cors.Default())

	auth.AuthController(router, DB, FB)
	auth.OTPController(router, DB, FB)
	auth.CaptchaController(router, DB, FB)

	board.BoardController(router, DB, FB)
	board.CreateBoardController(router, DB, FB)
	board.DeleteBoardController(router, DB, FB)

	admin.AdminController(router, DB, FB)

	report.ReportController(router, DB, FB)

	task.TaskController(router, DB, FB)
	task.FinishTaskController(router, DB, FB)
	task.CreateTaskController(router, DB, FB)
	task.TodayTaskController(router, DB, FB)
	task.UpdateTaskController(router, DB, FB)
	task.DeleteTaskController(router, DB, FB)

	assigned.AssignedController(router, DB, FB)

	notification.NotificationTaskController(router, DB, FB)
	notification.SendNotificationTaskController(router, DB, FB)
	notification.RemindNotificationTaskController(router, DB, FB)

	checklist.CreateChecklistController(router, DB, FB)
	checklist.UpdateChecklistController(router, DB, FB)
	checklist.DeleteChecklistController(router, DB, FB)
	checklist.FinishChecklistController(router, DB, FB)

	attachments.AttachmentsController(router, DB, FB)

	shareboard.ShareboardController(router, DB, FB)

	controller.GetemailCTL(router, DB)
	user.UserController(router, DB, FB)

	router.Run()
}
