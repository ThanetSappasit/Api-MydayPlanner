// scheduler/scheduler.go
package scheduler

import (
	"log"
	"mydayplanner/connection"
	"mydayplanner/controller/notification"

	"github.com/robfig/cron/v3"
)

func StartScheduler() {
	c := cron.New(cron.WithSeconds()) // รองรับ seconds ด้วย

	// เชื่อมต่อ database
	DB, err := connection.DBConnection()
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}

	FB, err := connection.FBConnection()
	if err != nil {
		log.Fatalf("Failed to initialize Firestore client: %v", err)
	}

	// กำหนด cron job - ทุกนาที
	_, err = c.AddFunc("0 * * * * *", func() {
		log.Println("Running scheduled notification job...")
		notification.SendNotificationJob(DB, FB)
	})

	if err != nil {
		log.Fatalf("Failed to add cron job: %v", err)
	}

	c.Start()
	log.Println("Scheduler started")

	// Block forever
	select {}
}
