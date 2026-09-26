package controllers

import (
	"encoding/json"
	"fmt"
	"log"
	"student_request_review_service/db"
)

func UpdateInstructorResponse(body map[string]interface{}) (string, error) {

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
	     "message": "Instructor response updated successfully on student end."
	   }
	    OR

	   {
	     "message": "Failed to update instructor response in database on student end."
	   }
	*/
	log.Println("UpdateInstructorResponse: invoked with body:", body)

	// Extract data from input: username FROM JWT (logged-in instructor)
	username, ok := body["username"].(string)
	if !ok {
		err := fmt.Errorf("missing or invalid username")
		log.Println("UpdateInstructorResponse: error extracting username:", err)
		return "", err
	}

	// Query for the logged-in user's (instructors) course_id
	log.Printf("UpdateInstructorResponse: querying course_id for instructor_name=%s", username)

	q := `
		SELECT course_id
		FROM instructors
		WHERE instructor_name = $1
		LIMIT 1
	`

	var courseID string
	err := db.DB.QueryRow(q, username).Scan(&courseID)
	if err != nil {
		log.Printf("UpdateInstructorResponse: course_id query error: %v", err)
		return "", fmt.Errorf("UpdateInstructorResponse: course_id query error: %v", err)
	}

	// this is the student AM to reply -> come from orchestrator request body.
	userID, ok := body["user_id"].(string)
	if !ok {
		err := fmt.Errorf("UpdateInstructorResponse: missing or invalid user_id (AM)")
		log.Println("UpdateInstructorResponse: error extracting user_id (AM):", err)
		return "", err
	}

	examPeriod, ok := body["exam_period"].(string)
	if !ok {
		return "", fmt.Errorf("missing exam_period")
	}

	instructorReply, ok := body["instructor_reply_message"].(string)
	if !ok {
		return "", fmt.Errorf("missing or invalid instructor_reply_message")
	}

	instructorAction, ok := body["instructor_action"].(string)
	if !ok {
		return "", fmt.Errorf("missing or invalid instructor_action")
	}
	log.Printf("UpdateInstructorResponse: updating review for student_id=%s, course_id=%s, exam_period=%s", userID, courseID, examPeriod)

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
		return "", fmt.Errorf("UpdateInstructorResponse failed to update review: %v", err)
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		return "", fmt.Errorf("review not found")
	}

	response := map[string]interface{}{
		"message": "Instructor response updated successfully on student end.",
	}
	respBytes, _ := json.Marshal(response)
	return string(respBytes), nil
}
