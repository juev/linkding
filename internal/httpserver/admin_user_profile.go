package httpserver

import (
	"context"
	"database/sql"
	"net/url"
	"strings"

	"github.com/juev/linkding/internal/settings"
)

type adminUserProfileChoice struct {
	Value, Label string
	Selected     bool
}

type adminUserProfileField struct {
	Name, Label, Kind, Value string
	Checked, Required        bool
	Choices                  []adminUserProfileChoice
}

type adminUserProfileData struct {
	ID, UserID        int64
	Fields            []adminUserProfileField
	CustomCSSHash     string
	SearchPreferences string
	Form              url.Values
}

func loadAdminUserProfile(ctx context.Context, db *sql.DB, engine string, userID int64) (adminUserProfileData, error) {
	form, err := settings.LoadProfileForm(ctx, db, engine, userID)
	if err != nil {
		return adminUserProfileData{}, err
	}
	var profile adminUserProfileData
	query := `SELECT id,custom_css_hash,search_preferences FROM bookmarks_userprofile WHERE user_id = ` + assetMarker(engine, 1)
	if err := db.QueryRowContext(ctx, query, userID).Scan(&profile.ID, &profile.CustomCSSHash, &profile.SearchPreferences); err != nil {
		return adminUserProfileData{}, err
	}
	profile.UserID, profile.Form = userID, form
	profile.buildFields()
	return profile, nil
}

func adminUserProfileFromPost(values url.Values, profile adminUserProfileData) adminUserProfileData {
	form := url.Values{}
	for _, field := range settings.ProfileFields {
		form.Set(field.Name, values.Get("profile-0-"+field.Name))
	}
	profile.Form = form
	profile.CustomCSSHash = values.Get("profile-0-custom_css_hash")
	profile.buildFields()
	return profile
}

func (profile *adminUserProfileData) buildFields() {
	profile.Fields = nil
	byName := make(map[string]settings.ProfileField, len(settings.ProfileFields))
	for _, field := range settings.ProfileFields {
		byName[field.Name] = field
	}
	for _, name := range profileFieldOrder {
		definition := byName[name]
		value := profile.Form.Get(name)
		label := profileLabels[name]
		if label == "" {
			label = strings.ReplaceAll(name, "_", " ")
			label = strings.ToUpper(label[:1]) + label[1:]
		}
		field := adminUserProfileField{Name: "profile-0-" + name, Label: label, Kind: definition.Kind, Value: value, Checked: value != "" && value != "0" && !strings.EqualFold(value, "false"), Required: definition.Kind == "choice" || definition.Kind == "number"}
		for _, choice := range definition.Choices {
			field.Choices = append(field.Choices, adminUserProfileChoice{Value: choice, Label: choiceLabel(name, choice), Selected: value == choice})
		}
		profile.Fields = append(profile.Fields, field)
	}
}
