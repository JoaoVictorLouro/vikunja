// Vikunja is a to-do list application to facilitate your life.
// Copyright 2018-2021 Vikunja and contributors. All rights reserved.
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public Licensee as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU Affero General Public Licensee for more details.
//
// You should have received a copy of the GNU Affero General Public Licensee
// along with this program.  If not, see <https://www.gnu.org/licenses/>.

package models

import (
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"code.vikunja.io/api/pkg/config"
	"code.vikunja.io/api/pkg/db"
	"code.vikunja.io/api/pkg/events"
	"code.vikunja.io/api/pkg/log"
	"code.vikunja.io/api/pkg/user"
	"code.vikunja.io/web"

	"github.com/google/uuid"
	"github.com/imdario/mergo"
	"github.com/jinzhu/copier"
	"xorm.io/builder"
	"xorm.io/xorm"
	"xorm.io/xorm/schemas"
)

type TaskRepeatMode int

const (
	TaskRepeatModeDefault TaskRepeatMode = iota
	TaskRepeatModeMonth
	TaskRepeatModeFromCurrentDate
)

// Task represents an task in a project
type Task struct {
	// The unique, numeric id of this task.
	ID int64 `xorm:"bigint autoincr not null unique pk" json:"id" param:"projecttask"`
	// The task text. This is what you'll see in the project.
	Title string `xorm:"TEXT not null" json:"title" valid:"minstringlength(1)" minLength:"1"`
	// The task description.
	Description string `xorm:"longtext null" json:"description"`
	// Whether a task is done or not.
	Done bool `xorm:"INDEX null" json:"done"`
	// The time when a task was marked as done.
	DoneAt time.Time `xorm:"INDEX null 'done_at'" json:"done_at"`
	// The time when the task is due.
	DueDate time.Time `xorm:"DATETIME INDEX null 'due_date'" json:"due_date"`
	// An array of datetimes when the user wants to be reminded of the task.
	//
	// Deprecated: Use Reminders
	ReminderDates []time.Time `xorm:"-" json:"reminder_dates"`
	// An array of reminders that are associated with this task.
	Reminders []*TaskReminder `xorm:"-" json:"reminders"`
	// The project this task belongs to.
	ProjectID int64 `xorm:"bigint INDEX not null" json:"project_id" param:"project"`
	// An amount in seconds this task repeats itself. If this is set, when marking the task as done, it will mark itself as "undone" and then increase all remindes and the due date by its amount.
	RepeatAfter int64 `xorm:"bigint INDEX null" json:"repeat_after" valid:"range(0|9223372036854775807)"`
	// Can have three possible values which will trigger when the task is marked as done: 0 = repeats after the amount specified in repeat_after, 1 = repeats all dates each months (ignoring repeat_after), 3 = repeats from the current date rather than the last set date.
	RepeatMode TaskRepeatMode `xorm:"not null default 0" json:"repeat_mode"`
	// The task priority. Can be anything you want, it is possible to sort by this later.
	Priority int64 `xorm:"bigint null" json:"priority"`
	// When this task starts.
	StartDate time.Time `xorm:"DATETIME INDEX null 'start_date'" json:"start_date" query:"-"`
	// When this task ends.
	EndDate time.Time `xorm:"DATETIME INDEX null 'end_date'" json:"end_date" query:"-"`
	// An array of users who are assigned to this task
	Assignees []*user.User `xorm:"-" json:"assignees"`
	// An array of labels which are associated with this task.
	Labels []*Label `xorm:"-" json:"labels"`
	// The task color in hex
	HexColor string `xorm:"varchar(6) null" json:"hex_color" valid:"runelength(0|6)" maxLength:"6"`
	// Determines how far a task is left from being done
	PercentDone float64 `xorm:"DOUBLE null" json:"percent_done"`

	// The task identifier, based on the project identifier and the task's index
	Identifier string `xorm:"-" json:"identifier"`
	// The task index, calculated per project
	Index int64 `xorm:"bigint not null default 0" json:"index"`

	// The UID is currently not used for anything other than caldav, which is why we don't expose it over json
	UID string `xorm:"varchar(250) null" json:"-"`

	// All related tasks, grouped by their relation kind
	RelatedTasks RelatedTaskMap `xorm:"-" json:"related_tasks"`

	// All attachments this task has
	Attachments []*TaskAttachment `xorm:"-" json:"attachments"`

	// If this task has a cover image, the field will return the id of the attachment that is the cover image.
	CoverImageAttachmentID int64 `xorm:"bigint default 0" json:"cover_image_attachment_id"`

	// True if a task is a favorite task. Favorite tasks show up in a separate "Important" project. This value depends on the user making the call to the api.
	IsFavorite bool `xorm:"-" json:"is_favorite"`

	// The subscription status for the user reading this task. You can only read this property, use the subscription endpoints to modify it.
	// Will only returned when retreiving one task.
	Subscription *Subscription `xorm:"-" json:"subscription,omitempty"`

	// A timestamp when this task was created. You cannot change this value.
	Created time.Time `xorm:"created not null" json:"created"`
	// A timestamp when this task was last updated. You cannot change this value.
	Updated time.Time `xorm:"updated not null" json:"updated"`

	// BucketID is the ID of the kanban bucket this task belongs to.
	BucketID int64 `xorm:"bigint null" json:"bucket_id"`

	// The position of the task - any task project can be sorted as usual by this parameter.
	// When accessing tasks via kanban buckets, this is primarily used to sort them based on a range
	// We're using a float64 here to make it possible to put any task within any two other tasks (by changing the number).
	// You would calculate the new position between two tasks with something like task3.position = (task2.position - task1.position) / 2.
	// A 64-Bit float leaves plenty of room to initially give tasks a position with 2^16 difference to the previous task
	// which also leaves a lot of room for rearranging and sorting later.
	Position float64 `xorm:"double null" json:"position"`
	// The position of tasks in the kanban board. See the docs for the `position` property on how to use this.
	KanbanPosition float64 `xorm:"double null" json:"kanban_position"`

	// The user who initially created the task.
	CreatedBy   *user.User `xorm:"-" json:"created_by" valid:"-"`
	CreatedByID int64      `xorm:"bigint not null" json:"-"` // ID of the user who put that task on the project

	web.CRUDable `xorm:"-" json:"-"`
	web.Rights   `xorm:"-" json:"-"`
}

type TaskWithComments struct {
	Task
	Comments []*TaskComment `xorm:"-" json:"comments"`
}

// TableName returns the table name for tasks
func (*Task) TableName() string {
	return "tasks"
}

// GetFullIdentifier returns the task identifier if the task has one and the index prefixed with # otherwise.
func (t *Task) GetFullIdentifier() string {
	if t.Identifier != "" {
		return t.Identifier
	}

	return "#" + strconv.FormatInt(t.Index, 10)
}

func (t *Task) GetFrontendURL() string {
	return config.ServiceFrontendurl.GetString() + "tasks/" + strconv.FormatInt(t.ID, 10)
}

type taskFilterConcatinator string

const (
	filterConcatAnd = "and"
	filterConcatOr  = "or"
)

type taskOptions struct {
	search             string
	page               int
	perPage            int
	sortby             []*sortParam
	filters            []*taskFilter
	filterConcat       taskFilterConcatinator
	filterIncludeNulls bool
}

// ReadAll is a dummy function to still have that endpoint documented
// @Summary Get tasks
// @Description Returns all tasks on any project the user has access to.
// @tags task
// @Accept json
// @Produce json
// @Param page query int false "The page number. Used for pagination. If not provided, the first page of results is returned."
// @Param per_page query int false "The maximum number of items per page. Note this parameter is limited by the configured maximum of items per page."
// @Param s query string false "Search tasks by task text."
// @Param sort_by query string false "The sorting parameter. You can pass this multiple times to get the tasks ordered by multiple different parametes, along with `order_by`. Possible values to sort by are `id`, `title`, `description`, `done`, `done_at`, `due_date`, `created_by_id`, `project_id`, `repeat_after`, `priority`, `start_date`, `end_date`, `hex_color`, `percent_done`, `uid`, `created`, `updated`. Default is `id`."
// @Param order_by query string false "The ordering parameter. Possible values to order by are `asc` or `desc`. Default is `asc`."
// @Param filter_by query string false "The name of the field to filter by. Allowed values are all task properties. Task properties which are their own object require passing in the id of that entity. Accepts an array for multiple filters which will be chanied together, all supplied filter must match."
// @Param filter_value query string false "The value to filter for."
// @Param filter_comparator query string false "The comparator to use for a filter. Available values are `equals`, `greater`, `greater_equals`, `less`, `less_equals`, `like` and `in`. `in` expects comma-separated values in `filter_value`. Defaults to `equals`"
// @Param filter_concat query string false "The concatinator to use for filters. Available values are `and` or `or`. Defaults to `or`."
// @Param filter_include_nulls query string false "If set to true the result will include filtered fields whose value is set to `null`. Available values are `true` or `false`. Defaults to `false`."
// @Security JWTKeyAuth
// @Success 200 {array} models.Task "The tasks"
// @Failure 500 {object} models.Message "Internal error"
// @Router /tasks/all [get]
func (t *Task) ReadAll(_ *xorm.Session, _ web.Auth, _ string, _ int, _ int) (result interface{}, resultCount int, totalItems int64, err error) {
	return nil, 0, 0, nil
}

func getFilterCond(f *taskFilter, includeNulls bool) (cond builder.Cond, err error) {
	field := "`" + f.field + "`"
	switch f.comparator {
	case taskFilterComparatorEquals:
		cond = &builder.Eq{field: f.value}
	case taskFilterComparatorNotEquals:
		cond = &builder.Neq{field: f.value}
	case taskFilterComparatorGreater:
		cond = &builder.Gt{field: f.value}
	case taskFilterComparatorGreateEquals:
		cond = &builder.Gte{field: f.value}
	case taskFilterComparatorLess:
		cond = &builder.Lt{field: f.value}
	case taskFilterComparatorLessEquals:
		cond = &builder.Lte{field: f.value}
	case taskFilterComparatorLike:
		val, is := f.value.(string)
		if !is {
			return nil, ErrInvalidTaskFilterValue{Field: field, Value: f.value}
		}
		cond = &builder.Like{field, "%" + val + "%"}
	case taskFilterComparatorIn:
		cond = builder.In(field, f.value)
	case taskFilterComparatorInvalid:
		// Nothing to do
	}

	if includeNulls {
		cond = builder.Or(cond, &builder.IsNull{field})
		if f.isNumeric {
			cond = builder.Or(cond, &builder.IsNull{field}, &builder.Eq{field: 0})
		}
	}

	return
}

func getFilterCondForSeparateTable(table string, concat taskFilterConcatinator, conds []builder.Cond) builder.Cond {
	var filtercond builder.Cond
	if concat == filterConcatOr {
		filtercond = builder.Or(conds...)
	}
	if concat == filterConcatAnd {
		filtercond = builder.And(conds...)
	}

	return builder.In(
		"id",
		builder.
			Select("task_id").
			From(table).
			Where(filtercond),
	)
}

func getTaskIndexFromSearchString(s string) (index int64) {
	re := regexp.MustCompile("#([0-9]+)")
	in := re.FindString(s)

	stringIndex := strings.ReplaceAll(in, "#", "")
	index, _ = strconv.ParseInt(stringIndex, 10, 64)
	return
}

//nolint:gocyclo
func getRawTasksForProjects(s *xorm.Session, projects []*Project, a web.Auth, opts *taskOptions) (tasks []*Task, resultCount int, totalItems int64, err error) {

	// If the user does not have any projects, don't try to get any tasks
	if len(projects) == 0 {
		return nil, 0, 0, nil
	}

	// Set the default concatinator of filter variables to or if none was provided
	if opts.filterConcat == "" {
		opts.filterConcat = filterConcatOr
	}

	// Get all project IDs and get the tasks
	var projectIDs []int64
	var hasFavoritesProject bool
	for _, l := range projects {
		if l.ID == FavoritesPseudoProject.ID {
			hasFavoritesProject = true
			continue
		}
		projectIDs = append(projectIDs, l.ID)
	}

	// Add the id parameter as the last parameter to sorty by default, but only if it is not already passed as the last parameter.
	if len(opts.sortby) == 0 ||
		len(opts.sortby) > 0 && opts.sortby[len(opts.sortby)-1].sortBy != taskPropertyID {
		opts.sortby = append(opts.sortby, &sortParam{
			sortBy:  taskPropertyID,
			orderBy: orderAscending,
		})
	}

	// Since xorm does not use placeholders for order by, it is possible to expose this with sql injection if we're directly
	// passing user input to the db.
	// As a workaround to prevent this, we check for valid column names here prior to passing it to the db.
	var orderby string
	for i, param := range opts.sortby {
		// Validate the params
		if err := param.validate(); err != nil {
			return nil, 0, 0, err
		}

		// Mysql sorts columns with null values before ones without null value.
		// Because it does not have support for NULLS FIRST or NULLS LAST we work around this by
		// first sorting for null (or not null) values and then the order we actually want to.
		if db.Type() == schemas.MYSQL {
			orderby += "`" + param.sortBy + "` IS NULL, "
		}

		orderby += "`" + param.sortBy + "` " + param.orderBy.String()

		// Postgres and sqlite allow us to control how columns with null values are sorted.
		// To make that consistent with the sort order we have and other dbms, we're adding a separate clause here.
		if db.Type() == schemas.POSTGRES || db.Type() == schemas.SQLITE {
			orderby += " NULLS LAST"
		}

		if (i + 1) < len(opts.sortby) {
			orderby += ", "
		}
	}

	// Some filters need a special treatment since they are in a separate table
	reminderFilters := []builder.Cond{}
	assigneeFilters := []builder.Cond{}
	labelFilters := []builder.Cond{}
	namespaceFilters := []builder.Cond{}

	var filters = make([]builder.Cond, 0, len(opts.filters))
	// To still find tasks with nil values, we exclude 0s when comparing with >/< values.
	for _, f := range opts.filters {
		if f.field == "reminders" {
			f.field = "reminder" // This is the name in the db
			filter, err := getFilterCond(f, opts.filterIncludeNulls)
			if err != nil {
				return nil, 0, 0, err
			}
			reminderFilters = append(reminderFilters, filter)
			continue
		}

		if f.field == "assignees" {
			if f.comparator == taskFilterComparatorLike {
				return nil, 0, 0, ErrInvalidTaskFilterValue{Field: f.field, Value: f.value}
			}
			f.field = "username"
			filter, err := getFilterCond(f, opts.filterIncludeNulls)
			if err != nil {
				return nil, 0, 0, err
			}
			assigneeFilters = append(assigneeFilters, filter)
			continue
		}

		if f.field == "labels" || f.field == "label_id" {
			f.field = "label_id"
			filter, err := getFilterCond(f, opts.filterIncludeNulls)
			if err != nil {
				return nil, 0, 0, err
			}
			labelFilters = append(labelFilters, filter)
			continue
		}

		if f.field == "namespace" || f.field == "namespace_id" {
			f.field = "namespace_id"
			filter, err := getFilterCond(f, opts.filterIncludeNulls)
			if err != nil {
				return nil, 0, 0, err
			}
			namespaceFilters = append(namespaceFilters, filter)
			continue
		}

		filter, err := getFilterCond(f, opts.filterIncludeNulls)
		if err != nil {
			return nil, 0, 0, err
		}
		filters = append(filters, filter)
	}

	// Then return all tasks for that projects
	var where builder.Cond

	if opts.search != "" {
		where = db.ILIKE("title", opts.search)

		searchIndex := getTaskIndexFromSearchString(opts.search)
		if searchIndex > 0 {
			where = builder.Or(where, builder.Eq{"`index`": searchIndex})
		}
	}

	var projectIDCond builder.Cond
	var projectCond builder.Cond
	if len(projectIDs) > 0 {
		projectIDCond = builder.In("project_id", projectIDs)
		projectCond = projectIDCond
	}

	if hasFavoritesProject {
		// Make sure users can only see their favorites
		userProjects, _, _, err := getRawProjectsForUser(
			s,
			&projectOptions{
				user: &user.User{ID: a.GetID()},
				page: -1,
			},
		)
		if err != nil {
			return nil, 0, 0, err
		}

		userProjectIDs := make([]int64, 0, len(userProjects))
		for _, l := range userProjects {
			userProjectIDs = append(userProjectIDs, l.ID)
		}

		// All favorite tasks for that user
		favCond := builder.
			Select("entity_id").
			From("favorites").
			Where(
				builder.And(
					builder.Eq{"user_id": a.GetID()},
					builder.Eq{"kind": FavoriteKindTask},
				))

		projectCond = builder.And(projectCond, builder.And(builder.In("id", favCond), builder.In("project_id", userProjectIDs)))
	}

	if len(reminderFilters) > 0 {
		filters = append(filters, getFilterCondForSeparateTable("task_reminders", opts.filterConcat, reminderFilters))
	}

	if len(assigneeFilters) > 0 {
		assigneeFilter := []builder.Cond{
			builder.In("user_id",
				builder.Select("id").
					From("users").
					Where(builder.Or(assigneeFilters...)),
			)}
		filters = append(filters, getFilterCondForSeparateTable("task_assignees", opts.filterConcat, assigneeFilter))
	}

	if len(labelFilters) > 0 {
		filters = append(filters, getFilterCondForSeparateTable("label_tasks", opts.filterConcat, labelFilters))
	}

	if len(namespaceFilters) > 0 {
		var filtercond builder.Cond
		if opts.filterConcat == filterConcatOr {
			filtercond = builder.Or(namespaceFilters...)
		}
		if opts.filterConcat == filterConcatAnd {
			filtercond = builder.And(namespaceFilters...)
		}

		cond := builder.In(
			"project_id",
			builder.
				Select("id").
				From("projects").
				Where(filtercond),
		)
		filters = append(filters, cond)
	}

	var filterCond builder.Cond
	if len(filters) > 0 {
		if opts.filterConcat == filterConcatOr {
			filterCond = builder.Or(filters...)
		}
		if opts.filterConcat == filterConcatAnd {
			filterCond = builder.And(filters...)
		}
	}

	limit, start := getLimitFromPageIndex(opts.page, opts.perPage)
	cond := builder.And(projectCond, where, filterCond)

	query := s.Where(cond)
	if limit > 0 {
		query = query.Limit(limit, start)
	}

	tasks = []*Task{}
	err = query.OrderBy(orderby).Find(&tasks)
	if err != nil {
		return nil, 0, 0, err
	}

	queryCount := s.Where(cond)
	totalItems, err = queryCount.
		Count(&Task{})
	if err != nil {
		return nil, 0, 0, err
	}

	return tasks, len(tasks), totalItems, nil
}

func getTasksForProjects(s *xorm.Session, projects []*Project, a web.Auth, opts *taskOptions) (tasks []*Task, resultCount int, totalItems int64, err error) {

	tasks, resultCount, totalItems, err = getRawTasksForProjects(s, projects, a, opts)
	if err != nil {
		return nil, 0, 0, err
	}

	taskMap := make(map[int64]*Task, len(tasks))
	for _, t := range tasks {
		taskMap[t.ID] = t
	}

	err = addMoreInfoToTasks(s, taskMap, a)
	if err != nil {
		return nil, 0, 0, err
	}

	return tasks, resultCount, totalItems, err
}

// GetTaskByIDSimple returns a raw task without extra data by the task ID
func GetTaskByIDSimple(s *xorm.Session, taskID int64) (task Task, err error) {
	if taskID < 1 {
		return Task{}, ErrTaskDoesNotExist{taskID}
	}

	return GetTaskSimple(s, &Task{ID: taskID})
}

// GetTaskSimple returns a raw task without extra data
func GetTaskSimple(s *xorm.Session, t *Task) (task Task, err error) {
	task = *t
	exists, err := s.Get(&task)
	if err != nil {
		return Task{}, err
	}

	if !exists {
		return Task{}, ErrTaskDoesNotExist{t.ID}
	}
	return
}

// GetTasksByIDs returns all tasks for a project of ids
func (bt *BulkTask) GetTasksByIDs(s *xorm.Session) (err error) {
	for _, id := range bt.IDs {
		if id < 1 {
			return ErrTaskDoesNotExist{id}
		}
	}

	err = s.In("id", bt.IDs).Find(&bt.Tasks)
	if err != nil {
		return
	}

	return
}

// GetTasksByUIDs gets all tasks from a bunch of uids
func GetTasksByUIDs(s *xorm.Session, uids []string, a web.Auth) (tasks []*Task, err error) {
	tasks = []*Task{}
	err = s.In("uid", uids).Find(&tasks)
	if err != nil {
		return
	}

	taskMap := make(map[int64]*Task, len(tasks))
	for _, t := range tasks {
		taskMap[t.ID] = t
	}

	err = addMoreInfoToTasks(s, taskMap, a)
	return
}

func getRemindersForTasks(s *xorm.Session, taskIDs []int64) (reminders []*TaskReminder, err error) {
	reminders = []*TaskReminder{}
	err = s.In("task_id", taskIDs).
		OrderBy("reminder asc").
		Find(&reminders)
	return
}

func (t *Task) setIdentifier(project *Project) {
	t.Identifier = project.Identifier + "-" + strconv.FormatInt(t.Index, 10)
}

// Get all assignees
func addAssigneesToTasks(s *xorm.Session, taskIDs []int64, taskMap map[int64]*Task) (err error) {
	taskAssignees, err := getRawTaskAssigneesForTasks(s, taskIDs)
	if err != nil {
		return
	}
	// Put the assignees in the task map
	for _, a := range taskAssignees {
		if a != nil {
			a.Email = "" // Obfuscate the email
			taskMap[a.TaskID].Assignees = append(taskMap[a.TaskID].Assignees, &a.User)
		}
	}

	return
}

// Get all labels for all the tasks
func addLabelsToTasks(s *xorm.Session, taskIDs []int64, taskMap map[int64]*Task) (err error) {
	labels, _, _, err := GetLabelsByTaskIDs(s, &LabelByTaskIDsOptions{
		TaskIDs: taskIDs,
		Page:    -1,
	})
	if err != nil {
		return
	}
	for _, l := range labels {
		if l != nil {
			taskMap[l.TaskID].Labels = append(taskMap[l.TaskID].Labels, &l.Label)
		}
	}

	return
}

// Get task attachments
func addAttachmentsToTasks(s *xorm.Session, taskIDs []int64, taskMap map[int64]*Task) (err error) {
	attachments, err := getTaskAttachmentsByTaskIDs(s, taskIDs)
	if err != nil {
		return
	}

	for _, a := range attachments {
		taskMap[a.TaskID].Attachments = append(taskMap[a.TaskID].Attachments, a)
	}
	return
}

func getTaskReminderMap(s *xorm.Session, taskIDs []int64) (taskReminders map[int64][]*TaskReminder, err error) {
	taskReminders = make(map[int64][]*TaskReminder)

	// Get all reminders and put them in a map to have it easier later
	reminders, err := getRemindersForTasks(s, taskIDs)
	if err != nil {
		return
	}

	for _, r := range reminders {
		taskReminders[r.TaskID] = append(taskReminders[r.TaskID], r)
	}

	return
}

func addRelatedTasksToTasks(s *xorm.Session, taskIDs []int64, taskMap map[int64]*Task, a web.Auth) (err error) {
	relatedTasks := []*TaskRelation{}
	err = s.In("task_id", taskIDs).Find(&relatedTasks)
	if err != nil {
		return
	}

	// Collect all related task IDs, so we can get all related task headers in one go
	var relatedTaskIDs []int64
	for _, rt := range relatedTasks {
		relatedTaskIDs = append(relatedTaskIDs, rt.OtherTaskID)
	}

	if len(relatedTaskIDs) == 0 {
		return
	}

	fullRelatedTasks := make(map[int64]*Task)
	err = s.In("id", relatedTaskIDs).Find(&fullRelatedTasks)
	if err != nil {
		return
	}

	taskFavorites, err := getFavorites(s, relatedTaskIDs, a, FavoriteKindTask)
	if err != nil {
		return err
	}

	// NOTE: while it certainly be possible to run this function on	fullRelatedTasks again, we don't do this for performance reasons.

	type permissionCheck struct {
		allowed bool
	}

	canViewTask := make(map[int64]*permissionCheck)

	// Go through all task relations and put them into the task objects
	for _, rt := range relatedTasks {
		_, has := fullRelatedTasks[rt.OtherTaskID]
		if !has {
			log.Debugf("Related task not found for task relation: taskID=%d, otherTaskID=%d, relationKind=%v", rt.TaskID, rt.OtherTaskID, rt.RelationKind)
			continue
		}
		fullRelatedTasks[rt.OtherTaskID].IsFavorite = taskFavorites[rt.OtherTaskID]

		_, has = canViewTask[rt.OtherTaskID]
		if !has {
			p := Project{ID: fullRelatedTasks[rt.OtherTaskID].ProjectID}
			can, _, err := p.CanRead(s, a)
			if err != nil {
				return err
			}

			canViewTask[rt.OtherTaskID] = &permissionCheck{allowed: can}
		}
		check := canViewTask[rt.OtherTaskID]
		if !check.allowed {
			continue
		}

		// We're duplicating the other task to avoid cycles as these can't be represented properly in json
		// and would thus fail with an error.
		otherTask := &Task{}
		err = copier.Copy(otherTask, fullRelatedTasks[rt.OtherTaskID])
		if err != nil {
			log.Errorf("Could not duplicate task object: %v", err)
			continue
		}
		otherTask.RelatedTasks = nil
		taskMap[rt.TaskID].RelatedTasks[rt.RelationKind] = append(taskMap[rt.TaskID].RelatedTasks[rt.RelationKind], otherTask)
	}

	return
}

// This function takes a map with pointers and returns a slice with pointers to tasks
// It adds more stuff like assignees/labels/etc to a bunch of tasks
func addMoreInfoToTasks(s *xorm.Session, taskMap map[int64]*Task, a web.Auth) (err error) {

	// No need to iterate over users and stuff if the project doesn't have tasks
	if len(taskMap) == 0 {
		return
	}

	// Get all users & task ids and put them into the array
	var userIDs []int64
	var taskIDs []int64
	var projectIDs []int64
	for _, i := range taskMap {
		taskIDs = append(taskIDs, i.ID)
		userIDs = append(userIDs, i.CreatedByID)
		projectIDs = append(projectIDs, i.ProjectID)
	}

	err = addAssigneesToTasks(s, taskIDs, taskMap)
	if err != nil {
		return
	}

	err = addLabelsToTasks(s, taskIDs, taskMap)
	if err != nil {
		return
	}

	err = addAttachmentsToTasks(s, taskIDs, taskMap)
	if err != nil {
		return
	}

	users, err := getUsersOrLinkSharesFromIDs(s, userIDs)
	if err != nil {
		return
	}

	taskReminders, err := getTaskReminderMap(s, taskIDs)
	if err != nil {
		return err
	}

	taskFavorites, err := getFavorites(s, taskIDs, a, FavoriteKindTask)
	if err != nil {
		return err
	}

	// Get all identifiers
	projects, err := GetProjectsByIDs(s, projectIDs)
	if err != nil {
		return err
	}

	// Add all objects to their tasks
	for _, task := range taskMap {

		// Make created by user objects
		task.CreatedBy = users[task.CreatedByID]

		// Add the reminder dates (Remove, when ReminderDates is removed)
		for _, r := range taskReminders[task.ID] {
			task.ReminderDates = append(task.ReminderDates, r.Reminder)
		}

		// Add the reminders
		task.Reminders = taskReminders[task.ID]

		// Prepare the subtasks
		task.RelatedTasks = make(RelatedTaskMap)

		// Build the task identifier from the project identifier and task index
		task.setIdentifier(projects[task.ProjectID])

		task.IsFavorite = taskFavorites[task.ID]
	}

	// Get all related tasks
	err = addRelatedTasksToTasks(s, taskIDs, taskMap, a)
	return
}

func checkBucketAndTaskBelongToSameProject(fullTask *Task, bucket *Bucket) (err error) {
	if fullTask.ProjectID != bucket.ProjectID {
		return ErrBucketDoesNotBelongToProject{
			ProjectID: fullTask.ProjectID,
			BucketID:  fullTask.BucketID,
		}
	}

	return
}

// Checks if adding a new task would exceed the bucket limit
func checkBucketLimit(s *xorm.Session, t *Task, bucket *Bucket) (err error) {
	if bucket.Limit > 0 {
		taskCount, err := s.
			Where("bucket_id = ?", bucket.ID).
			Count(&Task{})
		if err != nil {
			return err
		}
		if taskCount >= bucket.Limit {
			return ErrBucketLimitExceeded{TaskID: t.ID, BucketID: bucket.ID, Limit: bucket.Limit}
		}
	}
	return nil
}

// Contains all the task logic to figure out what bucket to use for this task.
func setTaskBucket(s *xorm.Session, task *Task, originalTask *Task, doCheckBucketLimit bool) (targetBucket *Bucket, err error) {
	// Make sure we have a bucket
	var bucket *Bucket
	if task.Done && originalTask != nil && !originalTask.Done {
		bucket, err := getDoneBucketForProject(s, task.ProjectID)
		if err != nil {
			return nil, err
		}
		if bucket != nil {
			task.BucketID = bucket.ID
		}
	}

	if task.BucketID == 0 && originalTask != nil && originalTask.BucketID != 0 {
		task.BucketID = originalTask.BucketID
	}

	// Either no bucket was provided or the task was moved between projects
	if task.BucketID == 0 || (originalTask != nil && task.ProjectID != 0 && originalTask.ProjectID != task.ProjectID) {
		bucket, err = getDefaultBucket(s, task.ProjectID)
		if err != nil {
			return
		}
		task.BucketID = bucket.ID
	}

	if bucket == nil {
		bucket, err = getBucketByID(s, task.BucketID)
		if err != nil {
			return
		}
	}

	// If there is a bucket set, make sure they belong to the same project as the task
	err = checkBucketAndTaskBelongToSameProject(task, bucket)
	if err != nil {
		return
	}

	// Check the bucket limit
	// Only check the bucket limit if the task is being moved between buckets, allow reordering the task within a bucket
	if doCheckBucketLimit {
		if err := checkBucketLimit(s, task, bucket); err != nil {
			return nil, err
		}
	}

	if bucket.IsDoneBucket && originalTask != nil && !originalTask.Done {
		task.Done = true
	}

	return bucket, nil
}

func calculateDefaultPosition(entityID int64, position float64) float64 {
	if position == 0 {
		return float64(entityID) * math.Pow(2, 16)
	}

	return position
}

func getNextTaskIndex(s *xorm.Session, projectID int64) (nextIndex int64, err error) {
	latestTask := &Task{}
	_, err = s.
		Where("project_id = ?", projectID).
		OrderBy("`index` desc").
		Get(latestTask)
	if err != nil {
		return 0, err
	}

	return latestTask.Index + 1, nil
}

// Create is the implementation to create a project task
// @Summary Create a task
// @Description Inserts a task into a project.
// @tags task
// @Accept json
// @Produce json
// @Security JWTKeyAuth
// @Param id path int true "Project ID"
// @Param task body models.Task true "The task object"
// @Success 201 {object} models.Task "The created task object."
// @Failure 400 {object} web.HTTPError "Invalid task object provided."
// @Failure 403 {object} web.HTTPError "The user does not have access to the project"
// @Failure 500 {object} models.Message "Internal error"
// @Router /projects/{id} [put]
func (t *Task) Create(s *xorm.Session, a web.Auth) (err error) {
	return createTask(s, t, a, true)
}

func createTask(s *xorm.Session, t *Task, a web.Auth, updateAssignees bool) (err error) {

	t.ID = 0

	// Check if we have at least a text
	if t.Title == "" {
		return ErrTaskCannotBeEmpty{}
	}

	// Check if the project exists
	l, err := GetProjectSimpleByID(s, t.ProjectID)
	if err != nil {
		return err
	}

	createdBy, err := GetUserOrLinkShareUser(s, a)
	if err != nil {
		return err
	}
	t.CreatedByID = createdBy.ID

	// Generate a uuid if we don't already have one
	if t.UID == "" {
		t.UID = uuid.NewString()
	}

	// Get the default bucket and move the task there
	_, err = setTaskBucket(s, t, nil, true)
	if err != nil {
		return
	}

	// Get the index for this task
	t.Index, err = getNextTaskIndex(s, t.ProjectID)
	if err != nil {
		return err
	}

	// If no position was supplied, set a default one
	t.Position = calculateDefaultPosition(t.Index, t.Position)
	t.KanbanPosition = calculateDefaultPosition(t.Index, t.KanbanPosition)
	if _, err = s.Insert(t); err != nil {
		return err
	}

	t.CreatedBy = createdBy

	// Update the assignees
	if updateAssignees {
		if err := t.updateTaskAssignees(s, t.Assignees, a); err != nil {
			return err
		}
	}

	// Update the reminders
	if err := t.updateReminders(s, t); err != nil {
		return err
	}

	t.setIdentifier(l)

	if t.IsFavorite {
		if err := addToFavorites(s, t.ID, createdBy, FavoriteKindTask); err != nil {
			return err
		}
	}

	err = events.Dispatch(&TaskCreatedEvent{
		Task: t,
		Doer: createdBy,
	})
	if err != nil {
		return err
	}

	err = updateProjectLastUpdated(s, &Project{ID: t.ProjectID})
	return
}

// Update updates a project task
// @Summary Update a task
// @Description Updates a task. This includes marking it as done. Assignees you pass will be updated, see their individual endpoints for more details on how this is done. To update labels, see the description of the endpoint.
// @tags task
// @Accept json
// @Produce json
// @Security JWTKeyAuth
// @Param id path int true "Task ID"
// @Param task body models.Task true "The task object"
// @Success 200 {object} models.Task "The updated task object."
// @Failure 400 {object} web.HTTPError "Invalid task object provided."
// @Failure 403 {object} web.HTTPError "The user does not have access to the task (aka its project)"
// @Failure 500 {object} models.Message "Internal error"
// @Router /tasks/{id} [post]
//
//nolint:gocyclo
func (t *Task) Update(s *xorm.Session, a web.Auth) (err error) {

	// Check if the task exists and get the old values
	ot, err := GetTaskByIDSimple(s, t.ID)
	if err != nil {
		return
	}

	if t.ProjectID == 0 {
		t.ProjectID = ot.ProjectID
	}

	// Get the stored reminders
	reminders, err := getRemindersForTasks(s, []int64{t.ID})
	if err != nil {
		return
	}

	// Old task has the stored reminders
	ot.Reminders = reminders

	// Deprecated: remove when ReminderDates is removed
	ot.ReminderDates = make([]time.Time, len(reminders))
	for i, r := range reminders {
		ot.ReminderDates[i] = r.Reminder
	}

	targetBucket, err := setTaskBucket(s, t, &ot, t.BucketID != 0 && t.BucketID != ot.BucketID)
	if err != nil {
		return err
	}

	// If the task was moved into the done bucket and the task has a repeating cycle we should not update
	// the bucket.
	if targetBucket.IsDoneBucket && t.RepeatAfter > 0 {
		t.Done = true // This will trigger the correct re-scheduling of the task (happening in updateDone later)
		t.BucketID = ot.BucketID
	}

	// When a repeating task is marked as done, we update all deadlines and reminders and set it as undone
	updateDone(&ot, t)

	// Update the assignees
	if err := ot.updateTaskAssignees(s, t.Assignees, a); err != nil {
		return err
	}

	// Update the reminders
	if err := ot.updateReminders(s, t); err != nil {
		return err
	}

	// All columns to update in a separate variable to be able to add to them
	colsToUpdate := []string{
		"title",
		"description",
		"done",
		"due_date",
		"repeat_after",
		"priority",
		"start_date",
		"end_date",
		"hex_color",
		"done_at",
		"percent_done",
		"project_id",
		"bucket_id",
		"position",
		"repeat_mode",
		"kanban_position",
		"cover_image_attachment_id",
	}

	// If the task is being moved between projects, make sure to move the bucket + index as well
	if t.ProjectID != 0 && ot.ProjectID != t.ProjectID {
		t.Index, err = getNextTaskIndex(s, t.ProjectID)
		if err != nil {
			return err
		}
		colsToUpdate = append(colsToUpdate, "index")
	}

	// If a task attachment is being set as cover image, check if the attachment actually belongs to the task
	if t.CoverImageAttachmentID != 0 {
		is, err := s.Exist(&TaskAttachment{
			TaskID: t.ID,
			ID:     t.CoverImageAttachmentID,
		})
		if err != nil {
			return err
		}
		if !is {
			return &ErrAttachmentDoesNotBelongToTask{
				AttachmentID: t.CoverImageAttachmentID,
				TaskID:       t.ID,
			}
		}
	}

	wasFavorite, err := isFavorite(s, t.ID, a, FavoriteKindTask)
	if err != nil {
		return
	}
	if t.IsFavorite && !wasFavorite {
		if err := addToFavorites(s, t.ID, a, FavoriteKindTask); err != nil {
			return err
		}
	}

	if !t.IsFavorite && wasFavorite {
		if err := removeFromFavorite(s, t.ID, a, FavoriteKindTask); err != nil {
			return err
		}
	}

	// Update the labels
	//
	// Maybe FIXME:
	// I've disabled this for now, because it requires significant changes in the way we do updates (using the
	// Update() function. We need a user object in updateTaskLabels to check if the user has the right to see
	// the label it is currently adding. To do this, we'll need to update the webhandler to let it pass the current
	// user object (like it's already the case with the create method). However when we change it, that'll break
	// a lot of existing code which we'll then need to refactor.
	// This is why.
	//
	// if err := ot.updateTaskLabels(t.Labels); err != nil {
	// 	return err
	// }
	// set the labels to ot.Labels because our updateTaskLabels function puts the full label objects in it pretty nicely
	// We also set this here to prevent it being overwritten later on.
	// t.Labels = ot.Labels

	// For whatever reason, xorm dont detect if done is updated, so we need to update this every time by hand
	// Which is why we merge the actual task struct with the one we got from the db
	// The user struct overrides values in the actual one.
	if err := mergo.Merge(&ot, t, mergo.WithOverride); err != nil {
		return err
	}

	//////
	// Mergo does ignore nil values. Because of that, we need to check all parameters and set the updated to
	// nil/their nil value in the struct which is inserted.
	////
	// Done
	if !t.Done {
		ot.Done = false
	}
	// Priority
	if t.Priority == 0 {
		ot.Priority = 0
	}
	// Description
	if t.Description == "" {
		ot.Description = ""
	}
	// Due date
	if t.DueDate.IsZero() {
		ot.DueDate = time.Time{}
	}
	// Repeat after
	if t.RepeatAfter == 0 {
		ot.RepeatAfter = 0
	}
	// Start date
	if t.StartDate.IsZero() {
		ot.StartDate = time.Time{}
	}
	// End date
	if t.EndDate.IsZero() {
		ot.EndDate = time.Time{}
	}
	// Color
	if t.HexColor == "" {
		ot.HexColor = ""
	}
	// Percent Done
	if t.PercentDone == 0 {
		ot.PercentDone = 0
	}
	// Position
	if t.Position == 0 {
		ot.Position = 0
	}
	if t.KanbanPosition == 0 {
		ot.KanbanPosition = 0
	}
	// Repeat from current date
	if t.RepeatMode == TaskRepeatModeDefault {
		ot.RepeatMode = TaskRepeatModeDefault
	}
	// Is Favorite
	if !t.IsFavorite {
		ot.IsFavorite = false
	}
	// Attachment cover image
	if t.CoverImageAttachmentID == 0 {
		ot.CoverImageAttachmentID = 0
	}

	_, err = s.ID(t.ID).
		Cols(colsToUpdate...).
		Update(ot)
	*t = ot
	if err != nil {
		return err
	}

	// Update all positions if the newly saved position is < 0.1
	if ot.Position < 0.1 {
		err = recalculateTaskPositions(s, t.ProjectID)
		if err != nil {
			return err
		}
	}
	if ot.KanbanPosition < 0.1 {
		err = recalculateTaskKanbanPositions(s, t.BucketID)
		if err != nil {
			return err
		}
	}

	// Get the task updated timestamp in a new struct - if we'd just try to put it into t which we already have, it
	// would still contain the old updated date.
	nt := &Task{}
	_, err = s.ID(t.ID).Get(nt)
	if err != nil {
		return err
	}
	t.Updated = nt.Updated
	t.Position = nt.Position
	t.KanbanPosition = nt.KanbanPosition

	doer, _ := user.GetFromAuth(a)
	err = events.Dispatch(&TaskUpdatedEvent{
		Task: t,
		Doer: doer,
	})
	if err != nil {
		return err
	}

	return updateProjectLastUpdated(s, &Project{ID: t.ProjectID})
}

func recalculateTaskKanbanPositions(s *xorm.Session, bucketID int64) (err error) {

	allTasks := []*Task{}
	err = s.
		Where("bucket_id = ?", bucketID).
		OrderBy("kanban_position asc").
		Find(&allTasks)
	if err != nil {
		return
	}

	maxPosition := math.Pow(2, 32)

	for i, task := range allTasks {

		currentPosition := maxPosition / float64(len(allTasks)) * (float64(i + 1))

		_, err = s.Cols("kanban_position").
			Where("id = ?", task.ID).
			Update(&Task{KanbanPosition: currentPosition})
		if err != nil {
			return
		}
	}

	return
}

func recalculateTaskPositions(s *xorm.Session, projectID int64) (err error) {

	allTasks := []*Task{}
	err = s.
		Where("project_id = ?", projectID).
		OrderBy("position asc").
		Find(&allTasks)
	if err != nil {
		return
	}

	maxPosition := math.Pow(2, 32)

	for i, task := range allTasks {

		currentPosition := maxPosition / float64(len(allTasks)) * (float64(i + 1))

		_, err = s.Cols("position").
			Where("id = ?", task.ID).
			Update(&Task{Position: currentPosition})
		if err != nil {
			return
		}
	}

	return
}

func addOneMonthToDate(d time.Time) time.Time {
	return time.Date(d.Year(), d.Month()+1, d.Day(), d.Hour(), d.Minute(), d.Second(), d.Nanosecond(), config.GetTimeZone())
}

func setTaskDatesDefault(oldTask, newTask *Task) {
	if oldTask.RepeatAfter == 0 {
		return
	}

	// Current time in an extra variable to base all calculations on the same time
	now := time.Now()

	repeatDuration := time.Duration(oldTask.RepeatAfter) * time.Second

	// assuming we'll merge the new task over the old task
	if !oldTask.DueDate.IsZero() {
		// Always add one instance of the repeating interval to catch cases where a due date is already in the future
		// but not the repeating interval
		newTask.DueDate = oldTask.DueDate.Add(repeatDuration)
		// Add the repeating interval until the new due date is in the future
		for !newTask.DueDate.After(now) {
			newTask.DueDate = newTask.DueDate.Add(repeatDuration)
		}
	}

	newTask.Reminders = oldTask.Reminders
	// When repeating from the current date, all reminders should keep their difference to each other.
	// To make this easier, we sort them first because we can then rely on the fact the first is the smallest
	if len(oldTask.Reminders) > 0 {
		for in, r := range oldTask.Reminders {
			newTask.Reminders[in].Reminder = r.Reminder.Add(repeatDuration)
			for !newTask.Reminders[in].Reminder.After(now) {
				newTask.Reminders[in].Reminder = newTask.Reminders[in].Reminder.Add(repeatDuration)
			}
		}
	}

	// If a task has a start and end date, the end date should keep the difference to the start date when setting them as new
	if !oldTask.StartDate.IsZero() {
		newTask.StartDate = oldTask.StartDate.Add(repeatDuration)
		for !newTask.StartDate.After(now) {
			newTask.StartDate = newTask.StartDate.Add(repeatDuration)
		}
	}

	if !oldTask.EndDate.IsZero() {
		newTask.EndDate = oldTask.EndDate.Add(repeatDuration)
		for !newTask.EndDate.After(now) {
			newTask.EndDate = newTask.EndDate.Add(repeatDuration)
		}
	}

	newTask.Done = false
}

func setTaskDatesMonthRepeat(oldTask, newTask *Task) {
	if !oldTask.DueDate.IsZero() {
		newTask.DueDate = addOneMonthToDate(oldTask.DueDate)
	}

	newTask.Reminders = oldTask.Reminders
	if len(oldTask.Reminders) > 0 {
		for in, r := range oldTask.Reminders {
			newTask.Reminders[in].Reminder = addOneMonthToDate(r.Reminder)
		}
	}

	if !oldTask.StartDate.IsZero() && !oldTask.EndDate.IsZero() {
		diff := oldTask.EndDate.Sub(oldTask.StartDate)
		newTask.StartDate = addOneMonthToDate(oldTask.StartDate)
		newTask.EndDate = newTask.StartDate.Add(diff)
	} else {
		if !oldTask.StartDate.IsZero() {
			newTask.StartDate = addOneMonthToDate(oldTask.StartDate)
		}

		if !oldTask.EndDate.IsZero() {
			newTask.EndDate = addOneMonthToDate(oldTask.EndDate)
		}
	}

	newTask.Done = false
}

func setTaskDatesFromCurrentDateRepeat(oldTask, newTask *Task) {
	if oldTask.RepeatAfter == 0 {
		return
	}

	// Current time in an extra variable to base all calculations on the same time
	now := time.Now()

	repeatDuration := time.Duration(oldTask.RepeatAfter) * time.Second

	// assuming we'll merge the new task over the old task
	if !oldTask.DueDate.IsZero() {
		newTask.DueDate = now.Add(repeatDuration)
	}

	newTask.Reminders = oldTask.Reminders
	// When repeating from the current date, all reminders should keep their difference to each other.
	// To make this easier, we sort them first because we can then rely on the fact the first is the smallest
	if len(oldTask.Reminders) > 0 {
		sort.Slice(oldTask.Reminders, func(i, j int) bool {
			return oldTask.Reminders[i].Reminder.Unix() < oldTask.Reminders[j].Reminder.Unix()
		})
		first := oldTask.Reminders[0].Reminder
		for in, r := range oldTask.Reminders {
			diff := r.Reminder.Sub(first)
			newTask.Reminders[in].Reminder = now.Add(repeatDuration + diff)
		}
	}

	// We want to preserve intervals among the due, start and end dates.
	// The due date is used as a reference point for all new dates, so the
	// behaviour depends on whether the due date is set at all.
	if oldTask.DueDate.IsZero() {
		// If a task has no due date, but does have a start and end date, the
		// end date should keep the difference to the start date when setting
		// them as new
		if !oldTask.StartDate.IsZero() && !oldTask.EndDate.IsZero() {
			diff := oldTask.EndDate.Sub(oldTask.StartDate)
			newTask.StartDate = now.Add(repeatDuration)
			newTask.EndDate = now.Add(repeatDuration + diff)
		} else {
			if !oldTask.StartDate.IsZero() {
				newTask.StartDate = now.Add(repeatDuration)
			}

			if !oldTask.EndDate.IsZero() {
				newTask.EndDate = now.Add(repeatDuration)
			}
		}
	} else {
		// If the old task has a start and due date, we set the new start date
		// to preserve the interval between them.
		if !oldTask.StartDate.IsZero() {
			diff := oldTask.DueDate.Sub(oldTask.StartDate)
			newTask.StartDate = newTask.DueDate.Add(-diff)
		}

		// If the old task has an end and due date, we set the new end date
		// to preserve the interval between them.
		if !oldTask.EndDate.IsZero() {
			diff := oldTask.DueDate.Sub(oldTask.EndDate)
			newTask.EndDate = newTask.DueDate.Add(-diff)
		}
	}

	newTask.Done = false
}

// This helper function updates the reminders, doneAt, start and end dates of the *old* task
// and saves the new values in the newTask object.
// We make a few assumptions here:
//  1. Everything in oldTask is the truth - we figure out if we update anything at all if oldTask.RepeatAfter has a value > 0
//  2. Because of 1., this functions should not be used to update values other than Done in the same go
func updateDone(oldTask *Task, newTask *Task) {
	if !oldTask.Done && newTask.Done {
		switch oldTask.RepeatMode {
		case TaskRepeatModeMonth:
			setTaskDatesMonthRepeat(oldTask, newTask)
		case TaskRepeatModeFromCurrentDate:
			setTaskDatesFromCurrentDateRepeat(oldTask, newTask)
		case TaskRepeatModeDefault:
			setTaskDatesDefault(oldTask, newTask)
		}

		newTask.DoneAt = time.Now()
	}

	// When unmarking a task as done, reset the timestamp
	if oldTask.Done && !newTask.Done {
		newTask.DoneAt = time.Time{}
	}
}

// Deprecated: will be removed when ReminderDates are removed from Task.
// For now the method just creates TaskReminder objects from the ReminderDates and overwrites Reminder.
func (t *Task) overwriteRemindersWithReminderDates(reminderDates []time.Time) {
	// If the client still sends old reminder_dates, then these will overwrite
	// the Reminders, if the were sent by the client, too.
	// We assume that clients still using the old API with reminder_dates do not understand the new reminders.
	// Clients who want to use the new Reminder structure must explicitey unset reminder_dates.

	// start with empty Reminders
	reminders := make([]*TaskReminder, 0)

	// append absolute triggers from ReminderDates
	for _, reminderDate := range reminderDates {
		reminders = append(reminders, &TaskReminder{TaskID: t.ID, Reminder: reminderDate})
	}
	t.Reminders = reminders
}

// Set the absolute trigger dates for Reminders with relative period
func updateRelativeReminderDates(task *Task) (err error) {
	for _, reminder := range task.Reminders {
		relativeDuration := time.Duration(reminder.RelativePeriod) * time.Second
		if reminder.RelativeTo != "" {
			reminder.Reminder = time.Time{}
		}
		switch reminder.RelativeTo {
		case ReminderRelationDueDate:
			if !task.DueDate.IsZero() {
				reminder.Reminder = task.DueDate.Add(relativeDuration)
			}
		case ReminderRelationStartDate:
			if !task.StartDate.IsZero() {
				reminder.Reminder = task.StartDate.Add(relativeDuration)
			}
		case ReminderRelationEndDate:
			if !task.EndDate.IsZero() {
				reminder.Reminder = task.EndDate.Add(relativeDuration)
			}
		default:
			if reminder.RelativePeriod != 0 {
				err = ErrReminderRelativeToMissing{
					TaskID: task.ID,
				}
				return err
			}
		}
	}
	return nil
}

// Removes all old reminders and adds the new ones. This is a lot easier and less buggy than
// trying to figure out which reminders changed and then only re-add those needed. And since it does
// not make a performance difference we'll just do that.
// The parameter is a slice which holds the new reminders.
func (t *Task) updateReminders(s *xorm.Session, task *Task) (err error) {

	// Deprecated: This statement must be removed when ReminderDates will be removed
	if task.ReminderDates != nil {
		task.overwriteRemindersWithReminderDates(task.ReminderDates)
	}

	_, err = s.
		Where("task_id = ?", t.ID).
		Delete(&TaskReminder{})
	if err != nil {
		return
	}

	err = updateRelativeReminderDates(task)
	if err != nil {
		return
	}

	// Resolve duplicates and sort them
	reminderMap := make(map[int64]*TaskReminder, len(task.Reminders))
	for _, reminder := range task.Reminders {
		reminderMap[reminder.Reminder.UTC().Unix()] = reminder
	}

	t.Reminders = make([]*TaskReminder, 0, len(reminderMap))
	t.ReminderDates = make([]time.Time, 0, len(reminderMap))

	// Loop through all reminders and add them
	for _, r := range reminderMap {
		taskReminder := &TaskReminder{
			TaskID:         t.ID,
			Reminder:       r.Reminder,
			RelativePeriod: r.RelativePeriod,
			RelativeTo:     r.RelativeTo}
		_, err = s.Insert(taskReminder)
		if err != nil {
			return err
		}
		t.Reminders = append(t.Reminders, taskReminder)
		t.ReminderDates = append(t.ReminderDates, taskReminder.Reminder)
	}

	// sort reminders
	sort.Slice(t.Reminders, func(i, j int) bool {
		return t.Reminders[i].Reminder.Before(t.Reminders[j].Reminder)
	})

	if len(t.Reminders) == 0 {
		t.Reminders = nil
		t.ReminderDates = nil
	}

	err = updateProjectLastUpdated(s, &Project{ID: t.ProjectID})
	return
}

func updateTaskLastUpdated(s *xorm.Session, task *Task) error {
	_, err := s.ID(task.ID).Cols("updated").Update(task)
	return err
}

// Delete implements the delete method for a task
// @Summary Delete a task
// @Description Deletes a task from a project. This does not mean "mark it done".
// @tags task
// @Produce json
// @Security JWTKeyAuth
// @Param id path int true "Task ID"
// @Success 200 {object} models.Message "The created task object."
// @Failure 400 {object} web.HTTPError "Invalid task ID provided."
// @Failure 403 {object} web.HTTPError "The user does not have access to the project"
// @Failure 500 {object} models.Message "Internal error"
// @Router /tasks/{id} [delete]
func (t *Task) Delete(s *xorm.Session, a web.Auth) (err error) {

	if _, err = s.ID(t.ID).Delete(Task{}); err != nil {
		return err
	}

	// Delete assignees
	if _, err = s.Where("task_id = ?", t.ID).Delete(TaskAssginee{}); err != nil {
		return err
	}

	// Delete Favorites
	err = removeFromFavorite(s, t.ID, a, FavoriteKindTask)
	if err != nil {
		return
	}

	// Delete label associations
	_, err = s.Where("task_id = ?", t.ID).Delete(&LabelTask{})
	if err != nil {
		return
	}

	// Delete task attachments
	attachments, err := getTaskAttachmentsByTaskIDs(s, []int64{t.ID})
	if err != nil {
		return err
	}
	for _, attachment := range attachments {
		// Using the attachment delete method here because that takes care of removing all files properly
		err = attachment.Delete(s, a)
		if err != nil && !IsErrTaskAttachmentDoesNotExist(err) {
			return err
		}
	}

	// Delete all comments
	_, err = s.Where("task_id = ?", t.ID).Delete(&TaskComment{})
	if err != nil {
		return
	}

	// Delete all relations
	_, err = s.Where("task_id = ? OR other_task_id = ?", t.ID, t.ID).Delete(&TaskRelation{})
	if err != nil {
		return
	}

	// Delete all reminders
	_, err = s.Where("task_id = ?", t.ID).Delete(&TaskReminder{})
	if err != nil {
		return
	}

	doer, _ := user.GetFromAuth(a)
	err = events.Dispatch(&TaskDeletedEvent{
		Task: t,
		Doer: doer,
	})
	if err != nil {
		return
	}

	err = updateProjectLastUpdated(s, &Project{ID: t.ProjectID})
	return
}

// ReadOne gets one task by its ID
// @Summary Get one task
// @Description Returns one task by its ID
// @tags task
// @Accept json
// @Produce json
// @Param ID path int true "The task ID"
// @Security JWTKeyAuth
// @Success 200 {object} models.Task "The task"
// @Failure 404 {object} models.Message "Task not found"
// @Failure 500 {object} models.Message "Internal error"
// @Router /tasks/{ID} [get]
func (t *Task) ReadOne(s *xorm.Session, a web.Auth) (err error) {

	*t, err = GetTaskByIDSimple(s, t.ID)
	if err != nil {
		return
	}
	taskMap := make(map[int64]*Task, 1)
	taskMap[t.ID] = t

	err = addMoreInfoToTasks(s, taskMap, a)
	if err != nil {
		return
	}

	if len(taskMap) == 0 {
		return ErrTaskDoesNotExist{t.ID}
	}

	*t = *taskMap[t.ID]

	t.Subscription, err = GetSubscription(s, SubscriptionEntityTask, t.ID, a)
	return
}
