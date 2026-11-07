package html2pdf

// Document contains static caller-generated HTML and ordered author stylesheets.
type Document struct {
	HTML        string
	Stylesheets []string
}
