package controllers

import (
	"encoding/json"
	"fmt"
	"instructor_review_reply_service/db"
)

func InsertStudentRequest(body map[string]interface{}) (string, error) {

	// input send by orchestrator in json form like:
	// {
	//   "body": {
	//     "exam_period": "spring 2025",
	//     "course_id": "101",
	//     "user_id": 42,
	//     "student_message": "Please recheck my assignment."
	//   }
	// }

	// extract data from input.
	courseID, ok := body["course_id"].(string)
	if !ok {
		return "", fmt.Errorf("missing or invalid course_id")
	}

	examPeriod, ok := body["exam_period"].(string)
	if !ok {
		return "", fmt.Errorf("missing exam_period")
	}

	userID, ok := body["user_id"].(string)
	if !ok {
		return "", fmt.Errorf("missing or invalid user_id")
	}

	studentMessage, ok := body["student_message"].(string)
	if !ok {
		return "", fmt.Errorf("missing or invalid student_message")
	}

	// add review to db
	query := `INSERT INTO reviews (student_id, course_id, exam_period, student_message) VALUES ($1, $2, $3, $4)`
	result, err := db.DB.Exec(query, userID, courseID, examPeriod, studentMessage)
	if err != nil {
		fmt.Println("Insert error:", err)
		return "", fmt.Errorf("failed to insert review")
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil || rowsAffected == 0 {
		return "", fmt.Errorf("failed to insert review")
	}

	// Return success response
	response := map[string]interface{}{
		"message": "Review request submitted successfully on instructor end.",
	}
	respBytes, _ := json.Marshal(response)
	return string(respBytes), nil
}
