package controllers

import (
	"encoding/json"
	"fmt"
	"instructor_review_reply_service/db"
	"log"
)

// PostReply processes instructor responses for a review request
func PostReply(body map[string]interface{}) (string, error) {
	log.Println("PostReply: invoked with body:", body)

	// Extract data from input: username FROM JWT (logged-in instructor)
	username, ok := body["username"].(string)
	if !ok {
		err := fmt.Errorf("missing or invalid username")
		log.Println("PostReply: error extracting username:", err)
		return "", err
	}

	// Query for the logged-in user's (instructors) course_id
	log.Printf("PostReply: querying course_id for instructor_name=%s", username)

	q := `
		SELECT course_id
		FROM instructors
		WHERE instructor_name = $1
		LIMIT 1
	`

	var courseID string
	err := db.DB.QueryRow(q, username).Scan(&courseID)
	if err != nil {
		log.Printf("PostReply: course_id query error: %v", err)
		return "", fmt.Errorf("PostReply: course_id query error: %v", err)
	}

	// this is the student AM to reply -> come from orchestrator request body.
	userID, ok := body["user_id"].(string)
	if !ok {
		err := fmt.Errorf("PostReply: missing or invalid user_id (AM)")
		log.Println("PostReply: error extracting user_id (AM):", err)
		return "", err
	}

	// Exam period, also comes from orch
	examPeriod, ok := body["exam_period"].(string)
	if !ok {
		err := fmt.Errorf("PostReply: missing or invalid exam_period")
		log.Println("PostReply: error extracting exam_period:", err)
		return "", err
	}

	instructorReply, ok := body["instructor_reply_message"].(string)
	if !ok {
		err := fmt.Errorf("PostReply: missing or invalid instructor_reply_message")
		log.Println("PostReply: error extracting instructor_reply_message:", err)
		return "", err
	}
	instructorAction, ok := body["instructor_action"].(string)
	if !ok {
		err := fmt.Errorf("PostReply: missing or invalid instructor_action")
		log.Println("PostReply: error extracting instructor_action:", err)
		return "", err
	}

	log.Printf("PostReply: updating review for student_id=%s, course_id=%s, exam_period=%s", userID, courseID, examPeriod)

	query := `
		UPDATE reviews 
		SET instructor_reply_message = $1,
			instructor_action = $2,
			status = 'reviewed',
			reviewed_at = CURRENT_TIMESTAMP
		WHERE student_id = $3 AND course_id = $4 AND exam_period = $5	
	`
	result, err := db.DB.Exec(query, instructorReply, instructorAction, userID, courseID, examPeriod)
	if err != nil {
		log.Printf("PostReply: update error: %v", err)
		return "", fmt.Errorf("PostReply: failed to update review: %v", err)
	}
	rowsAffected, _ := result.RowsAffected()
	log.Printf("PostReply: rows affected: %d", rowsAffected)
	if rowsAffected == 0 {
		log.Println("PostReply: no rows updated, possible invalid identifiers")
		log.Println("PostReply: returning failure response to orchestrator")
		return "", fmt.Errorf("review not found")
	}

	successResponse := map[string]interface{}{
		"message": "Instructor response updated successfully on instructor end.",
	}
	respBytes, err := json.Marshal(successResponse)
	if err != nil {
		log.Printf("PostReply: response marshal error: %v", err)
		return "", fmt.Errorf("failed to marshal response: %v", err)
	}
	log.Println("PostReply: returning success response to orchestrator")
	return string(respBytes), nil
}

/* EXAMPLE INPUT

{
  "username": "instructor",
  "user_id": "p3210001",
  "exam_period": "June 2025",
  "instructor_reply_message": "We will take your concerns into account for future assessments.",
  "instructor_action": "Will be considered"
}

EXAMPLE OUTPUT

{
  "message": "Instructor response updated successfully on instructor end."
}
 OR

{
  "message": "Failed to update instructor response in database on instructor end."
}
*/
