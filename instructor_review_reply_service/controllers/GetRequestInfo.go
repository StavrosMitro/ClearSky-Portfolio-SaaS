package controllers

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"instructor_review_reply_service/db"
)

func GetRequestInfo(body map[string]interface{}) (string, error) {

	// input send by orchestrator in json form like:
	//{
	//"body": {
	//  "course_id": "101",
	//  "exam_period": "spring 2025",
	//  "user_id": "42"
	//}

	// get data from json
	courseID, ok := body["course_id"].(string)
	if !ok {
		return "", fmt.Errorf("missing course_id")
	}

	userID, ok := body["user_id"].(string)
	if !ok {
		return "", fmt.Errorf("missing user_id")
	}

	examPeriod, ok := body["exam_period"].(string)
	if !ok {
		return "", fmt.Errorf("missing exam_period")
	}

	// search db using student_id & course_id & exam_period.
	query := `
		SELECT student_id, course_id, exam_period, student_message, review_created_at 
		FROM reviews 
		WHERE student_id = $1 AND course_id = $2 AND exam_period = $3`

	row := db.DB.QueryRow(query, userID, courseID, examPeriod)

	var review ReviewStruct
	err := row.Scan(
		&review.Student_id,
		&review.Course_id,
		&review.Exam_period,
		&review.Student_message,
		&review.Review_created_at,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", fmt.Errorf("review not found")
		}
		return "", fmt.Errorf("failed to read review")
	}
	resBytes, _ := json.Marshal(review)
	return string(resBytes), nil
}

/* func GetRequestInfo(c *gin.Context) {
	reviewIDStr := c.Param("review_id")
	reviewID, err := strconv.Atoi(reviewIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid review ID"})
		return
	}
	query := `
			SELECT
				student_id, course_id, student_message, status, instructor_reply_message,
				instructor_action, review_created_at, reviewed_at
			FROM reviews
			WHERE review_id = $1
		`

	row := db.DB.QueryRow(query, reviewID)

	var review ReviewStruct

	err = row.Scan(
		&review.Student_id,
		&review.Course_id,
		&review.Student_message,
		&review.Status,
		&review.Instructor_reply_message,
		&review.Instructor_action,
		&review.Review_created_at,
		&review.Reviewed_at,
	)
	log.Println("Scan error:", err)

	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Review not found"})
		return
	}

	c.JSON(http.StatusOK, review)

} */
