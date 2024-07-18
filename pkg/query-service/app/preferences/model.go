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
	Min int `json:"min"`
	Max int `json:"max"`
}

type Preference struct {
	Key           string        `json:"key"`
	Name          string        `json:"name"`
	Description   string        `json:"description"`
	ValueType     string        `json:"valueType"`
	DefaultValue  interface{}   `json:"defaultValue"`
	AllowedValues []interface{} `json:"allowedValues"`
	Range         Range         `json:"range"`
	AllowedScopes []string      `json:"allowedScopes"`
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

func GetOrgPreference(ctx context.Context, preferenceId string) (*PreferenceKV, *model.ApiError) {
	// check if the preference key exists or not
	preference, seen := preferenceMap[preferenceId]
	if !seen {
		return nil, &model.ApiError{Typ: model.ErrorBadData, Err: fmt.Errorf("no such preferenceId exists: %s", preferenceId)}
	}

	// check if the preference is enabled for org scope or not
	isPreferenceEnabled := false
	for _, scope := range preference.AllowedScopes {
		if scope == "org" {
			isPreferenceEnabled = true
		}
	}
	if !isPreferenceEnabled {
		return nil, &model.ApiError{Typ: model.ErrorForbidden, Err: fmt.Errorf("preference is not enabled at org scope: %s", preferenceId)}
	}

	// fetch the value from the database
	user := common.GetUserFromContext(ctx)
	var orgPreference PreferenceKV
	query := `SELECT preference_id , preference_value FROM org_preference WHERE preference_id=$1 AND org_id=$2;`
	err := db.Get(&orgPreference, query, preferenceId, user.OrgId)

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
	return &orgPreference, nil
}

func UpdateOrgPreference(ctx context.Context, preferenceId string, preferenceValue interface{}) (*PreferenceKV, *model.ApiError) {

	// check if the preference key exists or not
	preference, seen := preferenceMap[preferenceId]
	if !seen {
		return nil, &model.ApiError{Typ: model.ErrorBadData, Err: fmt.Errorf("no such preferenceId exists: %s", preferenceId)}
	}

	// check if the preference is enabled at org scope or not
	isPreferenceEnabled := false
	for _, scope := range preference.AllowedScopes {
		if scope == "org" {
			isPreferenceEnabled = true
		}
	}
	if !isPreferenceEnabled {
		return nil, &model.ApiError{Typ: model.ErrorForbidden, Err: fmt.Errorf("preference is not enabled at org scope: %s", preferenceId)}
	}

	// check if the preference value being provided is of preference valueType
	var typeOfValue string
	switch preferenceValue.(type) {
	case uint8, uint16, uint32, uint64, int, int8, int16, int32, int64:
		typeOfValue = "integer"
	case float32, float64:
		typeOfValue = "float"
	case string:
		typeOfValue = "string"
	case bool:
		typeOfValue = "boolean"
	default:
		typeOfValue = "unknown"
	}

	if typeOfValue != preference.ValueType {
		return nil, &model.ApiError{Typ: model.ErrorBadData, Err: fmt.Errorf("the preference value is not of expected type: %s", preference.ValueType)}
	}

	// check the validity of the value being part of allowed values or the range specified if any
	if preference.AllowedValues != nil {
		isInAllowedValues := false
		for _, value := range preference.AllowedValues {
			if value == preferenceValue {
				isInAllowedValues = true
			}
		}
		if !isInAllowedValues {
			return nil, &model.ApiError{Typ: model.ErrorBadData, Err: fmt.Errorf("the preference value is not in the list of allowedValues: %s", preference.AllowedValues...)}
		}
	} else {
		if preferenceValue.(int) < preference.Range.Min || preferenceValue.(int) > preference.Range.Max {
			return nil, &model.ApiError{Typ: model.ErrorBadData, Err: fmt.Errorf("the preference value is not in the range specified, min: %v , max:%v", preference.Range.Min, preference.Range.Max)}
		}
	}

	// update the values in the org_preference table and return the key and the value
	query := `INSERT INTO org_preference(preference_id,preference_value,org_id) VALUES($1,$2,$3)
	ON CONFLICT(preference_id,org_id) DO
	UPDATE SET preference_value= $2 WHERE preference_id=$1 AND org_id=$3;`

	user := common.GetUserFromContext(ctx)

	_, err := db.Exec(query, preferenceId, preferenceValue, user.OrgId)
	fmt.Println(user)
	if err != nil {
		return nil, &model.ApiError{Typ: model.ErrorExec, Err: fmt.Errorf("error in setting the preference value: %s", err.Error())}
	}

	return &PreferenceKV{
		PreferenceId:    preferenceId,
		PreferenceValue: preferenceValue,
	}, nil
}
