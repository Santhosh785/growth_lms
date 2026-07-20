// Package templates embeds the lightweight HTMX course-editor page.
package templates

import (
	"embed"
	"html/template"
)

//go:embed course_editor.html
var fs embed.FS

// CourseEditor is the parsed course-editor page template.
var CourseEditor = template.Must(template.ParseFS(fs, "course_editor.html"))
