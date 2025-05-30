package controller

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"cloud.google.com/go/firestore"
	"github.com/gin-gonic/gin"
	"google.golang.org/api/iterator"
	"gorm.io/gorm"
)

// OrphanedRequest represents the request payload
type OrphanedRequest struct {
	BoardEmail    string `json:"boardEmail" binding:"required"`
	GroupBoardID  string `json:"groupBoardId,omitempty"`
	DeleteOrphans bool   `json:"deleteOrphans,omitempty"`
}

// OrphanedItem represents an orphaned data item
type OrphanedItem struct {
	Path            string                 `json:"path"`
	Data            map[string]interface{} `json:"data"`
	TaskID          string                 `json:"taskId"`
	GroupBoardID    string                 `json:"groupBoardId"`
	DocumentID      string                 `json:"documentId"`
	MissingTaskPath string                 `json:"missingTaskPath"`
	Type            string                 `json:"type"` // "checklist" or "attachment"
}

// OrphanedResponse represents the response structure
type OrphanedResponse struct {
	Success      bool            `json:"success"`
	Message      string          `json:"message"`
	Checklists   []OrphanedItem  `json:"checklists"`
	Attachments  []OrphanedItem  `json:"attachments"`
	Summary      OrphanedSummary `json:"summary"`
	DeletedCount int             `json:"deletedCount,omitempty"`
}

// OrphanedSummary provides summary statistics
type OrphanedSummary struct {
	TotalOrphaned    int `json:"totalOrphaned"`
	ChecklistsCount  int `json:"checklistsCount"`
	AttachmentsCount int `json:"attachmentsCount"`
}

// PathComponents represents parsed Firestore path
type PathComponents struct {
	BoardEmail    string
	GroupBoardID  string
	TaskID        string
	SubCollection string
	DocumentID    string
}

func OrphanedController(router *gin.Engine, db *gorm.DB, firestoreClient *firestore.Client) {
	router.POST("/checkOrphaned", func(c *gin.Context) {
		Orphaned(c, db, firestoreClient)
	})
}

func Orphaned(c *gin.Context, db *gorm.DB, firestoreClient *firestore.Client) {
	ctx := context.Background()

	// Parse request
	var req OrphanedRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"error":   "Invalid request format: " + err.Error(),
		})
		return
	}

	// Validate email format
	if !strings.Contains(req.BoardEmail, "@") {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"error":   "Invalid board email format",
		})
		return
	}

	log.Printf("Checking orphaned data for board: %s, group: %s", req.BoardEmail, req.GroupBoardID)

	// Find orphaned data
	orphanedData, err := findOrphanedData(ctx, firestoreClient, req.BoardEmail, req.GroupBoardID)
	if err != nil {
		log.Printf("Error finding orphaned data: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"error":   "Failed to find orphaned data: " + err.Error(),
		})
		return
	}

	response := OrphanedResponse{
		Success:     true,
		Message:     "Orphaned data search completed successfully",
		Checklists:  orphanedData.Checklists,
		Attachments: orphanedData.Attachments,
		Summary:     orphanedData.Summary,
	}

	// Delete orphaned data if requested
	if req.DeleteOrphans && orphanedData.Summary.TotalOrphaned > 0 {
		deletedCount, err := deleteOrphanedData(ctx, firestoreClient, orphanedData)
		if err != nil {
			log.Printf("Error deleting orphaned data: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{
				"success": false,
				"error":   "Failed to delete orphaned data: " + err.Error(),
			})
			return
		}
		response.DeletedCount = deletedCount
		response.Message = fmt.Sprintf("Successfully deleted %d orphaned items", deletedCount)
	}

	c.JSON(http.StatusOK, response)
}

// findOrphanedData finds all orphaned checklists and attachments
func findOrphanedData(ctx context.Context, client *firestore.Client, boardEmail, groupBoardID string) (*OrphanedResponse, error) {
	result := &OrphanedResponse{
		Checklists:  make([]OrphanedItem, 0),
		Attachments: make([]OrphanedItem, 0),
	}

	// Get group boards to check
	groupBoardIDs, err := getGroupBoardIDs(ctx, client, boardEmail, groupBoardID)
	if err != nil {
		return nil, fmt.Errorf("failed to get group board IDs: %w", err)
	}

	for _, gid := range groupBoardIDs {
		// Get existing task IDs
		existingTaskIDs, err := getExistingTaskIDs(ctx, client, boardEmail, gid)
		if err != nil {
			return nil, fmt.Errorf("failed to get existing task IDs for group %s: %w", gid, err)
		}

		// Find orphaned checklists
		orphanedChecklists, err := findOrphanedSubcollection(ctx, client, boardEmail, gid, "Checklists", existingTaskIDs)
		if err != nil {
			return nil, fmt.Errorf("failed to find orphaned checklists: %w", err)
		}
		result.Checklists = append(result.Checklists, orphanedChecklists...)

		// Find orphaned attachments
		orphanedAttachments, err := findOrphanedSubcollection(ctx, client, boardEmail, gid, "Attachments", existingTaskIDs)
		if err != nil {
			return nil, fmt.Errorf("failed to find orphaned attachments: %w", err)
		}
		result.Attachments = append(result.Attachments, orphanedAttachments...)
	}

	// Update summary
	result.Summary.ChecklistsCount = len(result.Checklists)
	result.Summary.AttachmentsCount = len(result.Attachments)
	result.Summary.TotalOrphaned = result.Summary.ChecklistsCount + result.Summary.AttachmentsCount

	return result, nil
}

// getGroupBoardIDs returns list of group board IDs to check
func getGroupBoardIDs(ctx context.Context, client *firestore.Client, boardEmail, groupBoardID string) ([]string, error) {
	if groupBoardID != "" {
		// Check if specific group board exists
		doc := client.Collection("Boards").Doc(boardEmail).Collection("Group_Boards").Doc(groupBoardID)
		_, err := doc.Get(ctx)
		if err != nil {
			return nil, fmt.Errorf("group board %s not found: %w", groupBoardID, err)
		}
		return []string{groupBoardID}, nil
	}

	// Get all group boards
	var groupBoardIDs []string
	iter := client.Collection("Boards").Doc(boardEmail).Collection("Group_Boards").Documents(ctx)
	defer iter.Stop()

	for {
		doc, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("error iterating group boards: %w", err)
		}
		groupBoardIDs = append(groupBoardIDs, doc.Ref.ID)
	}

	return groupBoardIDs, nil
}

// getExistingTaskIDs returns set of existing task IDs for a group board
func getExistingTaskIDs(ctx context.Context, client *firestore.Client, boardEmail, groupBoardID string) (map[string]bool, error) {
	existingTaskIDs := make(map[string]bool)

	iter := client.Collection("Boards").Doc(boardEmail).
		Collection("Group_Boards").Doc(groupBoardID).
		Collection("tasks").Documents(ctx)
	defer iter.Stop()

	for {
		doc, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("error iterating tasks: %w", err)
		}
		existingTaskIDs[doc.Ref.ID] = true
	}

	return existingTaskIDs, nil
}

// findOrphanedSubcollection finds orphaned items in a specific subcollection
func findOrphanedSubcollection(ctx context.Context, client *firestore.Client, boardEmail, groupBoardID, subCollection string, existingTaskIDs map[string]bool) ([]OrphanedItem, error) {
	var orphanedItems []OrphanedItem

	// Use collection group query to find all items in the subcollection
	iter := client.CollectionGroup(subCollection).Documents(ctx)
	defer iter.Stop()

	for {
		doc, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("error iterating %s: %w", subCollection, err)
		}

		// Parse path to check if it belongs to our board/group
		pathComponents, err := parseFirestorePath(doc.Ref.Path)
		if err != nil {
			continue // Skip invalid paths
		}

		// Check if this document belongs to our board and group
		if pathComponents.BoardEmail != boardEmail || pathComponents.GroupBoardID != groupBoardID {
			continue
		}

		// Check if parent task exists
		if !existingTaskIDs[pathComponents.TaskID] {
			data := doc.Data()
			orphanedItems = append(orphanedItems, OrphanedItem{
				Path:            doc.Ref.Path,
				Data:            data,
				TaskID:          pathComponents.TaskID,
				GroupBoardID:    pathComponents.GroupBoardID,
				DocumentID:      pathComponents.DocumentID,
				MissingTaskPath: buildTaskPath(boardEmail, pathComponents.GroupBoardID, pathComponents.TaskID),
				Type:            strings.ToLower(subCollection[:len(subCollection)-1]), // Remove 's' from end
			})
		}
	}

	return orphanedItems, nil
}

// parseFirestorePath parses a Firestore document path
func parseFirestorePath(path string) (*PathComponents, error) {
	parts := strings.Split(path, "/")

	if len(parts) < 8 {
		return nil, fmt.Errorf("invalid path format: %s", path)
	}

	return &PathComponents{
		BoardEmail:    parts[1],
		GroupBoardID:  parts[3],
		TaskID:        parts[5],
		SubCollection: parts[6],
		DocumentID:    parts[7],
	}, nil
}

// buildTaskPath builds a task document path
func buildTaskPath(boardEmail, groupBoardID, taskID string) string {
	return fmt.Sprintf("Boards/%s/Group_Boards/%s/tasks/%s", boardEmail, groupBoardID, taskID)
}

// deleteOrphanedData deletes all orphaned items
func deleteOrphanedData(ctx context.Context, client *firestore.Client, orphanedData *OrphanedResponse) (int, error) {
	// Collect all orphaned items
	allOrphanedItems := make([]OrphanedItem, 0, len(orphanedData.Checklists)+len(orphanedData.Attachments))
	allOrphanedItems = append(allOrphanedItems, orphanedData.Checklists...)
	allOrphanedItems = append(allOrphanedItems, orphanedData.Attachments...)

	deletedCount := 0
	batchSize := 500 // Firestore batch limit

	for i := 0; i < len(allOrphanedItems); i += batchSize {
		end := i + batchSize
		if end > len(allOrphanedItems) {
			end = len(allOrphanedItems)
		}

		batch := client.Batch()
		for j := i; j < end; j++ {
			docRef := client.Doc(allOrphanedItems[j].Path)
			batch.Delete(docRef)
		}

		// Set timeout for batch operation
		batchCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		_, err := batch.Commit(batchCtx)
		cancel()

		if err != nil {
			return deletedCount, fmt.Errorf("failed to delete batch %d-%d: %w", i, end-1, err)
		}

		deletedCount += (end - i)
		log.Printf("Deleted batch %d-%d (%d items)", i, end-1, end-i)
	}

	return deletedCount, nil
}
