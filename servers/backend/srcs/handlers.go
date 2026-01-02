package main

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
)

// GetCategories handles GET /categories
func GetCategories(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	categories, err := GetCategoriesFromDB()
	if err != nil {
		http.Error(w, "Failed to retrieve categories", http.StatusInternalServerError)
		return
	}
	json.NewEncoder(w).Encode(categories)
}

// CreateCategory handles POST /categories
func CreateCategory(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	var req struct {
		Name    string   `json:"name"`
		Options []string `json:"options"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}

	if req.Name == "" || len(req.Options) == 0 {
		http.Error(w, "Name and options are required", http.StatusBadRequest)
		return
	}

	category, err := CreateCategoryInDB(req.Name, req.Options)
	if err != nil {
		http.Error(w, "Failed to create category", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(category)
}

// GetCategory handles GET /categories/:id
func GetCategory(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	idStr := strings.TrimPrefix(r.URL.Path, "/categories/")
	id, err := strconv.Atoi(idStr)
	if err != nil {
		http.Error(w, "Invalid category ID", http.StatusBadRequest)
		return
	}

	cat, err := GetCategoryFromDB(id)
	if err != nil {
		if err == ErrCategoryNotFound {
			http.Error(w, "Category not found", http.StatusNotFound)
		} else {
			http.Error(w, "Failed to retrieve category", http.StatusInternalServerError)
		}
		return
	}

	json.NewEncoder(w).Encode(cat)
}

// RecordVote handles POST /votes
func RecordVote(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	var voteReq VoteRequest
	if err := json.NewDecoder(r.Body).Decode(&voteReq); err != nil {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}

	if voteReq.CategoryID == 0 || voteReq.OptionID == 0 || voteReq.VoterID == "" {
		http.Error(w, "category_id, option_id, and voter_id are required", http.StatusBadRequest)
		return
	}

	vote, err := AddVoteToDatabase(voteReq.CategoryID, voteReq.CategoryID, voteReq.OptionID, voteReq.VoterID)
	if err != nil {
		switch err {
		case ErrCategoryNotFound:
			http.Error(w, "Category not found", http.StatusNotFound)
		case ErrOptionNotFound:
			http.Error(w, "Option not found", http.StatusNotFound)
		case ErrAlreadyVoted:
			http.Error(w, "You have already voted in this category", http.StatusConflict)
		default:
			http.Error(w, "Internal server error", http.StatusInternalServerError)
		}
		return
	}

	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(vote)
}

// GetResults handles GET /results/:id
func GetResults(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	idStr := strings.TrimPrefix(r.URL.Path, "/results/")
	id, err := strconv.Atoi(idStr)
	if err != nil {
		http.Error(w, "Invalid category ID", http.StatusBadRequest)
		return
	}

	results, err := GetResultsFromDB(id, id)
	if err != nil {
		http.Error(w, "Category not found", http.StatusNotFound)
		return
	}

	json.NewEncoder(w).Encode(results)
}
