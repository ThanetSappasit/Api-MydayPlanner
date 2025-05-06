package main

import (
	"mydayplanner/connection"

	"github.com/gin-gonic/gin"
)

func main() {
	gin.SetMode(gin.ReleaseMode)
	connection.StartServer()
}
