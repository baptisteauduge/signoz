package preferences

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"

	"github.com/jmoiron/sqlx"
	"go.signoz.io/signoz/ee/query-service/model"
	"go.signoz.io/signoz/pkg/query-service/common"
)

type Range struct {
	Min int64 `json:"min"`
	Max int64 `json:"max"`
}

type Preference struct {
	Key              string        `json:"key"`
	Name             string        `json:"name"`
	Description      string        `json:"description"`
	ValueType        string        `json:"valueType"`
	DefaultValue     interface{}   `json:"defaultValue"`
	AllowedValues    []interface{} `json:"allowedValues"`
	IsDescreteValues bool          `json:"isDescreteValues"`
	Range            Range         `json:"range"`
	AllowedScopes    []string      `json:"allowedScopes"`
}

func (p *Preference) ErrorValueTypeMismatch() *model.ApiError {
	return &model.ApiError{Typ: model.ErrorBadData, Err: fmt.Errorf("the preference value is not of expected type: %s", p.ValueType)}
}

const (
	PreferenceValueTypeInteger string = "integer"
	PreferenceValueTypeFloat   string = "float"
	PreferenceValueTypeString  string = "string"
	PreferenceValueTypeBoolean string = "boolean"
)

const (
	OrgAllowedScope  string = "org"
	UserAllowedScope string = "user"
)

func checkIfInAllowedValues(preferenceValue interface{}, preference *Preference) bool {
	isInAllowedValues := false
	for _, value := range preference.AllowedValues {
		if value == preferenceValue {
			isInAllowedValues = true
		}
	}
	return isInAllowedValues
}

func (p *Preference) IsValidValue(preferenceValue interface{}) *model.ApiError {
	switch p.ValueType {
	case PreferenceValueTypeInteger:
		val, ok := preferenceValue.(int64)
		if !ok {
			floatVal, ok := preferenceValue.(float64)
			if !ok || floatVal != float64(int64(floatVal)) {
				return p.ErrorValueTypeMismatch()
			}
			val = int64(floatVal)
		}
		if !p.IsDescreteValues {
			if val < p.Range.Min || val > p.Range.Max {
				return &model.ApiError{Typ: model.ErrorBadData, Err: fmt.Errorf("the preference value is not in the range specified, min: %v , max:%v", p.Range.Min, p.Range.Max)}
			}
		}
	case PreferenceValueTypeString:
		_, ok := preferenceValue.(string)
		if !ok {
			return p.ErrorValueTypeMismatch()
		}
	case PreferenceValueTypeFloat:
		_, ok := preferenceValue.(float64)
		if !ok {
			return p.ErrorValueTypeMismatch()
		}
	case PreferenceValueTypeBoolean:
		_, ok := preferenceValue.(bool)
		if !ok {
			return p.ErrorValueTypeMismatch()
		}
	}

	// check the validity of the value being part of allowed values or the range specified if any
	if p.IsDescreteValues {
		if p.AllowedValues != nil {
			isInAllowedValues := checkIfInAllowedValues(preferenceValue, p)
			if !isInAllowedValues {
				return &model.ApiError{Typ: model.ErrorBadData, Err: fmt.Errorf("the preference value is not in the list of allowedValues: %v", p.AllowedValues)}
			}
		}
	}
	return nil
}

func (p *Preference) IsEnabledForScope(scope string) bool {
	isPreferenceEnabledForGivenScope := false
	if p.AllowedScopes != nil {
		for _, allowedScope := range p.AllowedScopes {
			if allowedScope == scope {
				isPreferenceEnabledForGivenScope = true
			}
		}
	}
	return isPreferenceEnabledForGivenScope
}

func (p *Preference) SanitizeValue(preferenceValue interface{}) interface{} {
	switch p.ValueType {
	case PreferenceValueTypeBoolean:
		if preferenceValue == "1" {
			return true
		} else {
			return false
		}
	default:
		return preferenceValue
	}
}

type AllPreferences struct {
	Key              string        `json:"key"`
	Name             string        `json:"name"`
	Description      string        `json:"description"`
	ValueType        string        `json:"valueType"`
	DefaultValue     interface{}   `json:"defaultValue"`
	Value            interface{}   `json:"value"`
	IsDescreteValues bool          `json:"isDescreteValues"`
	AllowedValues    []interface{} `json:"allowedValues"`
	Range            Range         `json:"range"`
	AllowedScopes    []string      `json:"allowedScopes"`
}

type PreferenceKV struct {
	PreferenceId    string      `json:"preference_id" db:"preference_id"`
	PreferenceValue interface{} `json:"preference_value" db:"preference_value"`
}

type UpdatePreference struct {
	PreferenceValue interface{} `json:"preference_value"`
}

var db *sqlx.DB

var preferences []Preference

var preferenceMap map[string]Preference

func InitDB(datasourceName string) error {
	var err error
	db, err = sqlx.Open("sqlite3", datasourceName)

	if err != nil {
		return err
	}

	// create the user preference table
	tableSchema := `
	PRAGMA foreign_keys = ON;
	CREATE TABLE IF NOT EXISTS user_preference(
		preference_id TEXT NOT NULL,
		preference_value TEXT,
		user_id TEXT NOT NULL,
		PRIMARY KEY (preference_id,user_id),
		FOREIGN KEY (user_id)
			REFERENCES users(id)
			ON UPDATE CASCADE
			ON DELETE CASCADE
	);`

	_, err = db.Exec(tableSchema)
	if err != nil {
		return fmt.Errorf("error in creating user_preference table: %s", err.Error())
	}

	// create the org preference table
	tableSchema = `
	PRAGMA foreign_keys = ON;
	CREATE TABLE IF NOT EXISTS org_preference(
		preference_id TEXT NOT NULL,
		preference_value TEXT,
		org_id TEXT NOT NULL,
		PRIMARY KEY (preference_id,org_id),
		FOREIGN KEY (org_id)
			REFERENCES organizations(id)
			ON UPDATE CASCADE
			ON DELETE CASCADE
	);`

	_, err = db.Exec(tableSchema)
	if err != nil {
		return fmt.Errorf("error in creating org_preference table: %s", err.Error())
	}

	preferenceFromFile, fileErr := fs.ReadFile(os.DirFS("../../pkg/query-service/app/preferences"), "preference.json")

	if fileErr != nil {
		return fmt.Errorf("error in reading preferences from file : %s", fileErr.Error())
	}

	if unmarshalErr := json.Unmarshal(preferenceFromFile, &preferences); unmarshalErr != nil {
		return fmt.Errorf("error in unmarshalling preferences from file : %s", unmarshalErr.Error())
	}

	preferenceMap = map[string]Preference{}
	for _, preference := range preferences {
		_, seen := preferenceMap[preference.Key]
		if seen {
			return fmt.Errorf("duplicate preference key in the preferences: %s", preference.Key)
		}
		preferenceMap[preference.Key] = preference
	}

	return nil
}

// org preference functions
func GetOrgPreference(ctx context.Context, preferenceId string, orgId string) (*PreferenceKV, *model.ApiError) {
	// check if the preference key exists or not
	preference, seen := preferenceMap[preferenceId]
	if !seen {
		return nil, &model.ApiError{Typ: model.ErrorBadData, Err: fmt.Errorf("no such preferenceId exists: %s", preferenceId)}
	}

	// check if the preference is enabled for org scope or not
	isPreferenceEnabled := preference.IsEnabledForScope(OrgAllowedScope)
	if !isPreferenceEnabled {
		return nil, &model.ApiError{Typ: model.ErrorForbidden, Err: fmt.Errorf("preference is not enabled at org scope: %s", preferenceId)}
	}

	// fetch the value from the database
	var orgPreference PreferenceKV
	query := `SELECT preference_id , preference_value FROM org_preference WHERE preference_id=$1 AND org_id=$2;`
	err := db.Get(&orgPreference, query, preferenceId, orgId)

	// if the value doesn't exist in db then return the default value
	if err == sql.ErrNoRows {
		return &PreferenceKV{
			PreferenceId:    preferenceId,
			PreferenceValue: preference.DefaultValue,
		}, nil
	} else if err != nil {
		return nil, &model.ApiError{Typ: model.ErrorExec, Err: fmt.Errorf("error in fetching the org preference: %s", err.Error())}
	}

	// else return the value fetched from the org_preference table
	return &PreferenceKV{
		PreferenceId:    preferenceId,
		PreferenceValue: preference.SanitizeValue(orgPreference.PreferenceValue),
	}, nil
}

func UpdateOrgPreference(ctx context.Context, preferenceId string, preferenceValue interface{}, orgId string) (*PreferenceKV, *model.ApiError) {
	// check if the preference key exists or not
	preference, seen := preferenceMap[preferenceId]
	if !seen {
		return nil, &model.ApiError{Typ: model.ErrorBadData, Err: fmt.Errorf("no such preferenceId exists: %s", preferenceId)}
	}

	// check if the preference is enabled at org scope or not
	isPreferenceEnabled := preference.IsEnabledForScope(OrgAllowedScope)
	if !isPreferenceEnabled {
		return nil, &model.ApiError{Typ: model.ErrorForbidden, Err: fmt.Errorf("preference is not enabled at org scope: %s", preferenceId)}
	}

	err := preference.IsValidValue(preferenceValue)
	if err != nil {
		return nil, err
	}

	// update the values in the org_preference table and return the key and the value
	query := `INSERT INTO org_preference(preference_id,preference_value,org_id) VALUES($1,$2,$3)
	ON CONFLICT(preference_id,org_id) DO
	UPDATE SET preference_value= $2 WHERE preference_id=$1 AND org_id=$3;`

	_, dberr := db.Exec(query, preferenceId, preferenceValue, orgId)

	if dberr != nil {
		return nil, &model.ApiError{Typ: model.ErrorExec, Err: fmt.Errorf("error in setting the preference value: %s", dberr.Error())}
	}

	return &PreferenceKV{
		PreferenceId:    preferenceId,
		PreferenceValue: preferenceValue,
	}, nil
}

func GetAllOrgPreferences(ctx context.Context, orgId string) (*[]AllPreferences, *model.ApiError) {
	// filter out all the org enabled preferences from the preference variable
	allOrgPreferences := []AllPreferences{}

	// fetch all the org preference values stored in org_preference table
	orgPreferenceValues := []PreferenceKV{}

	query := `SELECT preference_id,preference_value FROM org_preference WHERE org_id=$1;`
	err := db.Select(&orgPreferenceValues, query, orgId)

	if err != nil {
		return nil, &model.ApiError{Typ: model.ErrorExec, Err: fmt.Errorf("error in getting all org preference values: %s", err)}
	}

	// create a map of key vs values from the above response
	preferenceValueMap := map[string]interface{}{}

	for _, preferenceValue := range orgPreferenceValues {
		preferenceValueMap[preferenceValue.PreferenceId] = preferenceValue.PreferenceValue
	}

	// update in the above filtered list wherver value present in the map
	for _, preference := range preferences {
		isEnabledForOrgScope := preference.IsEnabledForScope(OrgAllowedScope)
		preferenceWithValue := AllPreferences{}
		preferenceWithValue.Key = preference.Key
		preferenceWithValue.Name = preference.Name
		preferenceWithValue.Description = preference.Description
		preferenceWithValue.AllowedScopes = preference.AllowedScopes
		preferenceWithValue.AllowedValues = preference.AllowedValues
		preferenceWithValue.DefaultValue = preference.DefaultValue
		preferenceWithValue.Range = preference.Range
		preferenceWithValue.ValueType = preference.ValueType
		preferenceWithValue.IsDescreteValues = preference.IsDescreteValues

		if isEnabledForOrgScope {
			ok, seen := preferenceValueMap[preference.Key]

			if seen {
				preferenceWithValue.Value = ok
			} else {
				preferenceWithValue.Value = preference.DefaultValue
			}

			preferenceWithValue.Value = preference.SanitizeValue(preferenceWithValue.Value)
			allOrgPreferences = append(allOrgPreferences, preferenceWithValue)
		}
	}
	return &allOrgPreferences, nil
}

// user preference functions
func GetUserPreference(ctx context.Context, preferenceId string, orgId string) (*PreferenceKV, *model.ApiError) {

	user := common.GetUserFromContext(ctx)
	// check if the preference key exists
	preference, seen := preferenceMap[preferenceId]
	if !seen {
		return nil, &model.ApiError{Typ: model.ErrorBadData, Err: fmt.Errorf("no such preferenceId exists: %s", preferenceId)}
	}

	preferenceValue := PreferenceKV{
		PreferenceId:    preferenceId,
		PreferenceValue: preference.DefaultValue,
	}

	// check if the preference is enabled at user scope
	isPreferenceEnabledAtUserScope := preference.IsEnabledForScope(UserAllowedScope)
	isPreferenceEnabledAtOrgScope := preference.IsEnabledForScope(OrgAllowedScope)
	if !isPreferenceEnabledAtUserScope {
		return nil, &model.ApiError{Typ: model.ErrorForbidden, Err: fmt.Errorf("preference is not enabled at user scope: %s", preferenceId)}
	}

	// get the value from the org scope if enabled at org scope
	if isPreferenceEnabledAtOrgScope {
		orgPreference := PreferenceKV{}

		query := `SELECT preference_id , preference_value FROM org_preference WHERE preference_id=$1 AND org_id=$2;`

		err := db.Get(&orgPreference, query, preferenceId, orgId)

		// if there is error in getting values and its not an empty rows error return from here
		if err != nil && err != sql.ErrNoRows {
			return nil, &model.ApiError{Typ: model.ErrorExec, Err: fmt.Errorf("error in getting org preference values: %s", err.Error())}
		}

		// if there is no error update the preference value with value from org preference
		if err == nil {
			preferenceValue.PreferenceValue = orgPreference.PreferenceValue
		}
	}

	// get the value from the user_preference table, if exists return this value else the one calculated in the above step
	userPreference := PreferenceKV{}

	query := `SELECT preference_id, preference_value FROM user_preference WHERE preference_id=$1 AND user_id=$2;`
	err := db.Get(&userPreference, query, preferenceId, user.User.Id)

	if err != nil && err != sql.ErrNoRows {
		return nil, &model.ApiError{Typ: model.ErrorExec, Err: fmt.Errorf("error in getting user preference values: %s", err.Error())}
	}

	if err == nil {
		preferenceValue.PreferenceValue = userPreference.PreferenceValue
	}

	return &PreferenceKV{
		PreferenceId:    preferenceValue.PreferenceId,
		PreferenceValue: preference.SanitizeValue(preferenceValue.PreferenceValue),
	}, nil
}

func UpdateUserPreference(ctx context.Context, preferenceId string, preferenceValue interface{}) (*PreferenceKV, *model.ApiError) {
	user := common.GetUserFromContext(ctx)
	// check if the preference id is valid
	preference, seen := preferenceMap[preferenceId]
	if !seen {
		return nil, &model.ApiError{Typ: model.ErrorBadData, Err: fmt.Errorf("no such preferenceId exists: %s", preferenceId)}
	}

	// check if the preference is enabled at user scope
	isPreferenceEnabledAtUserScope := preference.IsEnabledForScope(UserAllowedScope)
	if !isPreferenceEnabledAtUserScope {
		return nil, &model.ApiError{Typ: model.ErrorForbidden, Err: fmt.Errorf("preference is not enabled at user scope: %s", preferenceId)}
	}

	err := preference.IsValidValue(preferenceValue)
	if err != nil {
		return nil, err
	}
	// update the user preference values
	query := `INSERT INTO user_preference(preference_id,preference_value,user_id) VALUES($1,$2,$3)
	ON CONFLICT(preference_id,user_id) DO
	UPDATE SET preference_value= $2 WHERE preference_id=$1 AND user_id=$3;`

	_, dberrr := db.Exec(query, preferenceId, preferenceValue, user.User.Id)

	if dberrr != nil {
		return nil, &model.ApiError{Typ: model.ErrorExec, Err: fmt.Errorf("error in setting the preference value: %s", dberrr.Error())}
	}

	return &PreferenceKV{
		PreferenceId:    preferenceId,
		PreferenceValue: preferenceValue,
	}, nil
}

func GetAllUserPreferences(ctx context.Context, orgId string) (*[]AllPreferences, *model.ApiError) {
	user := common.GetUserFromContext(ctx)
	allUserPreferences := []AllPreferences{}

	// fetch all the org preference values stored in org_preference table
	orgPreferenceValues := []PreferenceKV{}

	query := `SELECT preference_id,preference_value FROM org_preference WHERE org_id=$1;`
	err := db.Select(&orgPreferenceValues, query, orgId)

	if err != nil {
		return nil, &model.ApiError{Typ: model.ErrorExec, Err: fmt.Errorf("error in getting all org preference values: %s", err)}
	}

	// create a map of key vs values from the above response
	preferenceOrgValueMap := map[string]interface{}{}

	for _, preferenceValue := range orgPreferenceValues {
		preferenceOrgValueMap[preferenceValue.PreferenceId] = preferenceValue.PreferenceValue
	}

	// fetch all the user preference values stored in user_preference table
	userPreferenceValues := []PreferenceKV{}

	query = `SELECT preference_id,preference_value FROM user_preference WHERE user_id=$1;`
	err = db.Select(&userPreferenceValues, query, user.User.Id)

	if err != nil {
		return nil, &model.ApiError{Typ: model.ErrorExec, Err: fmt.Errorf("error in getting all user preference values: %s", err)}
	}

	// create a map of key vs values from the above response
	preferenceUserValueMap := map[string]interface{}{}

	for _, preferenceValue := range userPreferenceValues {
		preferenceUserValueMap[preferenceValue.PreferenceId] = preferenceValue.PreferenceValue
	}

	// update in the above filtered list wherver value present in the map
	for _, preference := range preferences {
		isEnabledForOrgScope := preference.IsEnabledForScope(OrgAllowedScope)
		isEnabledForUserScope := preference.IsEnabledForScope(UserAllowedScope)

		preferenceWithValue := AllPreferences{}
		preferenceWithValue.Key = preference.Key
		preferenceWithValue.Name = preference.Name
		preferenceWithValue.Description = preference.Description
		preferenceWithValue.AllowedScopes = preference.AllowedScopes
		preferenceWithValue.AllowedValues = preference.AllowedValues
		preferenceWithValue.DefaultValue = preference.DefaultValue
		preferenceWithValue.Range = preference.Range
		preferenceWithValue.ValueType = preference.ValueType
		preferenceWithValue.IsDescreteValues = preference.IsDescreteValues

		if isEnabledForUserScope {
			preferenceWithValue.Value = preference.DefaultValue

			if isEnabledForOrgScope {
				ok, seen := preferenceOrgValueMap[preference.Key]
				if seen {
					preferenceWithValue.Value = ok
				}
			}

			ok, seen := preferenceUserValueMap[preference.Key]

			if seen {
				preferenceWithValue.Value = ok
			}

			preferenceWithValue.Value = preference.SanitizeValue(preferenceWithValue.Value)
			allUserPreferences = append(allUserPreferences, preferenceWithValue)
		}
	}
	return &allUserPreferences, nil
}
