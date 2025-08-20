package main

import (
	"mydayplanner/connection"
	// "mydayplanner/scheduler"

	"github.com/gin-gonic/gin"
)

func main() {
	gin.SetMode(gin.ReleaseMode)
	// go scheduler.StartScheduler()
	connection.StartServer()
}
