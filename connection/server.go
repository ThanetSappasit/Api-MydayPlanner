package connection

import (
	"log"
	"mydayplanner/controller/admin"
	"mydayplanner/controller/auth"
	"mydayplanner/controller/board"
	"mydayplanner/controller/report"
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
	user.UserController(router, DB, FB)
	board.BoardController(router, DB, FB)
	admin.AdminController(router, DB, FB)
	report.ReportController(router, DB, FB)
	task.TaskController(router, DB, FB)

	router.Run()
}
