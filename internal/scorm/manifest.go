package scorm

import (
	"encoding/xml"
	"strconv"
	"strings"
)

// --- imsmanifest.xml binding ----------------------------------------------
//
// SCORM elements live in the imscp default namespace and the ADL adlcp/imsss
// namespaces. Go's encoding/xml matches an element/attribute tag that carries
// no namespace against the local name in ANY namespace, so the unprefixed tags
// below bind the default-namespace elements (organization, item, resource,
// title, ...) without us hard-coding a namespace URI that differs between 1.2
// and 2004. The genuinely prefixed bits (adlcp:scormtype on a resource,
// adlcp:masteryscore inside an item) are captured generically and read by
// local name, case-insensitively — 1.2 spells it "scormtype", 2004 "scormType".

type xmlManifest struct {
	XMLName       xml.Name         `xml:"manifest"`
	Identifier    string           `xml:"identifier,attr"`
	Metadata      xmlMetadata      `xml:"metadata"`
	Organizations xmlOrganizations `xml:"organizations"`
	Resources     []xmlResource    `xml:"resources>resource"`
}

type xmlMetadata struct {
	Schema        string `xml:"schema"`
	SchemaVersion string `xml:"schemaversion"`
}

type xmlOrganizations struct {
	Default       string            `xml:"default,attr"`
	Organizations []xmlOrganization `xml:"organization"`
}

type xmlOrganization struct {
	Identifier string    `xml:"identifier,attr"`
	Title      string    `xml:"title"`
	Items      []xmlItem `xml:"item"`
}

type xmlItem struct {
	Identifier    string    `xml:"identifier,attr"`
	IdentifierRef string    `xml:"identifierref,attr"`
	Title         string    `xml:"title"`
	MasteryScore  string    `xml:"masteryscore"`
	Items         []xmlItem `xml:"item"`
}

type xmlResource struct {
	Identifier string     `xml:"identifier,attr"`
	Type       string     `xml:"type,attr"`
	Href       string     `xml:"href,attr"`
	Attrs      []xml.Attr `xml:",any,attr"`
}

// scormType returns the resource's adlcp:scormtype/scormType attribute value
// (lower-cased), matched by local name regardless of prefix/namespace.
func (r xmlResource) scormType() string {
	for _, a := range r.Attrs {
		if strings.EqualFold(a.Name.Local, "scormtype") {
			return strings.ToLower(strings.TrimSpace(a.Value))
		}
	}
	return ""
}

// ParseManifest parses and validates an imsmanifest.xml, returning the
// normalized Package. It fails with a sentinel error (see scorm.go) when the
// XML is malformed, the version is undeterminable, there is no default
// organization, or nothing is launchable — so a handler can reject a bad
// upload with a specific 400 reason.
func ParseManifest(data []byte) (*Package, error) {
	if len(strings.TrimSpace(string(data))) == 0 {
		return nil, ErrEmptyManifest
	}

	var m xmlManifest
	if err := xml.Unmarshal(data, &m); err != nil {
		return nil, wrapf(ErrInvalidManifest, "%v", err)
	}
	if m.XMLName.Local != "manifest" {
		return nil, wrapf(ErrInvalidManifest, "root element is <%s>, expected <manifest>", m.XMLName.Local)
	}

	version, err := detectVersion(m.Metadata.SchemaVersion)
	if err != nil {
		return nil, err
	}

	org, ok := defaultOrganization(m.Organizations)
	if !ok {
		return nil, ErrNoDefaultOrganization
	}

	// Map resource identifier -> href for launch resolution.
	hrefByRef := make(map[string]string, len(m.Resources))
	for _, r := range m.Resources {
		if strings.TrimSpace(r.Href) != "" {
			hrefByRef[r.Identifier] = strings.TrimSpace(r.Href)
		}
	}

	items := convertItems(org.Items, hrefByRef)

	launch := firstLaunchHref(items)
	if launch == "" {
		// No item referenced a launchable resource; fall back to the first SCO
		// resource, then any resource with an href.
		launch = fallbackLaunchHref(m.Resources)
	}
	if launch == "" {
		return nil, ErrNoLaunchableResource
	}

	pkg := &Package{
		Version:        version,
		Identifier:     strings.TrimSpace(m.Identifier),
		OrganizationID: strings.TrimSpace(org.Identifier),
		Title:          normalizeSpace(org.Title),
		LaunchHref:     launch,
		MasteryScore:   firstMasteryScore(org.Items),
		Items:          items,
	}
	return pkg, nil
}

// detectVersion maps a metadata/schemaversion string to a Version. SCORM 1.2
// manifests report "1.2"; SCORM 2004 report a variant of "2004" ("2004 3rd
// Edition", "2004 4th Edition") or the older "CAM 1.3" / "1.3".
func detectVersion(schemaVersion string) (Version, error) {
	s := strings.ToLower(strings.TrimSpace(schemaVersion))
	switch {
	case s == "":
		return "", ErrUnknownVersion
	case strings.Contains(s, "1.2"):
		return Version12, nil
	case strings.Contains(s, "2004"), strings.Contains(s, "1.3"):
		return Version2004, nil
	default:
		return "", wrapf(ErrUnknownVersion, "schemaversion %q", schemaVersion)
	}
}

// defaultOrganization returns the organization named by organizations@default,
// or the first organization if the default is unset/unresolved.
func defaultOrganization(orgs xmlOrganizations) (xmlOrganization, bool) {
	if len(orgs.Organizations) == 0 {
		return xmlOrganization{}, false
	}
	if def := strings.TrimSpace(orgs.Default); def != "" {
		for _, o := range orgs.Organizations {
			if o.Identifier == def {
				return o, true
			}
		}
	}
	return orgs.Organizations[0], true
}

// convertItems maps the raw manifest item tree onto the exported Item tree,
// resolving each item's identifierref to its resource href.
func convertItems(raw []xmlItem, hrefByRef map[string]string) []Item {
	if len(raw) == 0 {
		return nil
	}
	out := make([]Item, 0, len(raw))
	for _, r := range raw {
		out = append(out, Item{
			Identifier:    strings.TrimSpace(r.Identifier),
			Title:         normalizeSpace(r.Title),
			IdentifierRef: strings.TrimSpace(r.IdentifierRef),
			LaunchHref:    hrefByRef[strings.TrimSpace(r.IdentifierRef)],
			Children:      convertItems(r.Items, hrefByRef),
		})
	}
	return out
}

// firstLaunchHref walks the item tree depth-first and returns the first item's
// resolved launch href.
func firstLaunchHref(items []Item) string {
	for _, it := range items {
		if it.LaunchHref != "" {
			return it.LaunchHref
		}
		if h := firstLaunchHref(it.Children); h != "" {
			return h
		}
	}
	return ""
}

// fallbackLaunchHref picks a launch href straight from the resources when no
// item references one: the first SCO resource with an href, else any resource
// with an href.
func fallbackLaunchHref(resources []xmlResource) string {
	for _, r := range resources {
		if r.scormType() == "sco" && strings.TrimSpace(r.Href) != "" {
			return strings.TrimSpace(r.Href)
		}
	}
	for _, r := range resources {
		if strings.TrimSpace(r.Href) != "" {
			return strings.TrimSpace(r.Href)
		}
	}
	return ""
}

// firstMasteryScore returns the first adlcp:masteryscore found in the item
// tree (SCORM 1.2), or nil when none is declared.
func firstMasteryScore(raw []xmlItem) *float64 {
	for _, r := range raw {
		if s := strings.TrimSpace(r.MasteryScore); s != "" {
			if v, err := strconv.ParseFloat(s, 64); err == nil {
				return &v
			}
		}
		if v := firstMasteryScore(r.Items); v != nil {
			return v
		}
	}
	return nil
}
