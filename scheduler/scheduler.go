// scheduler/scheduler.go
package scheduler

import (
	"log"
	"mydayplanner/connection"
	"mydayplanner/controller/notification"

	"github.com/robfig/cron/v3"
)

func StartScheduler() {
	c := cron.New(cron.WithSeconds()) // เปิดใช้ seconds

	// เชื่อมต่อ database
	DB, err := connection.DBConnection()
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}

	FB, err := connection.FBConnection()
	if err != nil {
		log.Fatalf("Failed to initialize Firestore client: %v", err)
	}

	// Job ที่รันทุกนาที (ใช้ seconds format: "0 * * * * *")
	if _, err = c.AddFunc("0 * * * * *", func() {
		log.Println("Running scheduled notification job...")
		notification.SendNotificationJob(DB, FB)
	}); err != nil {
		log.Fatalf("Failed to add SendNotificationJob cron: %v", err)
	}

	// Job ที่รันทุกชั่วโมง (ใช้ minutes format: "0 * * * *")
	if _, err := c.AddFunc("0 0 0 * * *", func() {
		log.Println("Running midnight daily task...")
		notification.RepeatNotificationJob(DB, FB)
	}); err != nil {
		log.Fatalf("Failed to add RepeatNotificationJob cron: %v", err)
	}

	c.Start()
	log.Println("Scheduler started")

	// Block forever
	select {}
}
