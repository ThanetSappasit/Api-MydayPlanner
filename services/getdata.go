package services

import (
	"mydayplanner/model"

	"gorm.io/gorm"
)

func GetUserdata(db *gorm.DB, userId string) (*model.User, error) {
	var user model.User
	if err := db.Where("user_id = ?", userId).First(&user).Error; err != nil {
		return nil, err
	}
	return &user, nil
}

func GetTaskData(db *gorm.DB, taskID string) (*model.Tasks, error) {
	var task model.Tasks
	if err := db.Where("task_id = ?", taskID).First(&task).Error; err != nil {
		return nil, err
	}
	return &task, nil
}
