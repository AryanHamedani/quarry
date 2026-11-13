package wp

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// RenderedField represents WordPress REST API fields that have `{ "rendered": "...", "protected": false }`,
// or sometimes just a raw string or boolean false depending on endpoint/plugin quirks.
type RenderedField struct {
	Rendered  string `json:"rendered"`
	Raw       string `json:"raw,omitempty"`
	Protected bool   `json:"protected,omitempty"`
}

func (r *RenderedField) UnmarshalJSON(data []byte) error {
	if len(data) == 0 || string(data) == "null" || string(data) == "false" {
		return nil
	}
	if data[0] == '"' {
		var s string
		if err := json.Unmarshal(data, &s); err != nil {
			return nil
		}
		r.Rendered = s
		return nil
	}
	var raw struct {
		Rendered  FlexString `json:"rendered"`
		Raw       FlexString `json:"raw"`
		Protected FlexBool   `json:"protected"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil
	}
	r.Rendered = string(raw.Rendered)
	r.Raw = string(raw.Raw)
	r.Protected = bool(raw.Protected)
	return nil
}

// WPTime handles WordPress ISO-8601 timestamps which often omit timezone offsets (e.g. "2026-11-13T14:22:05").
type WPTime struct {
	time.Time
}

func (t *WPTime) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return nil
	}
	s = strings.TrimSpace(s)
	if s == "" || strings.HasPrefix(s, "0000-00-00") {
		return nil
	}
	layouts := []string{
		"2006-01-02T15:04:05",
		time.RFC3339,
		"2006-01-02 15:04:05",
		"2006-01-02T15:04:05Z07:00",
	}
	for _, layout := range layouts {
		if parsed, err := time.Parse(layout, s); err == nil {
			t.Time = parsed
			return nil
		}
	}
	return nil
}

func (t WPTime) MarshalJSON() ([]byte, error) {
	if t.Time.IsZero() {
		return []byte(`""`), nil
	}
	return json.Marshal(t.Time.Format(time.RFC3339))
}

func (t WPTime) FormatShort() string {
	if t.Time.IsZero() {
		return "—"
	}
	return t.Time.Format("2006-01-02")
}

func (t WPTime) FormatFull() string {
	if t.Time.IsZero() {
		return "—"
	}
	return t.Time.Format("2006-01-02 15:04:05 UTC")
}

// SiteProfile holds discovered metadata and capabilities of a WordPress site.
type SiteProfile struct {
	Name            string                 `json:"name" yaml:"name"`
	Description     string                 `json:"description" yaml:"description"`
	URL             string                 `json:"url" yaml:"url"`
	Home            string                 `json:"home" yaml:"home"`
	GMTOffset       float64                `json:"gmt_offset" yaml:"gmt_offset"`
	TimezoneString  string                 `json:"timezone_string" yaml:"timezone_string"`
	Namespaces      []string               `json:"namespaces" yaml:"namespaces"`
	Authentication  map[string]any         `json:"authentication,omitempty" yaml:"authentication,omitempty"`
	Routes          map[string]RouteSchema `json:"routes,omitempty" yaml:"-"`
	PostTypes       map[string]PostType    `json:"post_types,omitempty" yaml:"post_types,omitempty"`
	Taxonomies      map[string]Taxonomy    `json:"taxonomies,omitempty" yaml:"taxonomies,omitempty"`
	APIRootURL      string                 `json:"api_root_url" yaml:"api_root_url"`
	SupportsPretty  bool                   `json:"supports_pretty_routes" yaml:"supports_pretty_routes"`
	DiscoveredAt    time.Time              `json:"discovered_at" yaml:"discovered_at"`
	ResponseLatency time.Duration          `json:"response_latency_ms" yaml:"response_latency_ms"`
	EntityCounts    map[string]int         `json:"entity_counts,omitempty" yaml:"entity_counts,omitempty"`
}

func (sp *SiteProfile) UnmarshalJSON(data []byte) error {
	var wire struct {
		Name           FlexString           `json:"name"`
		Description    FlexString           `json:"description"`
		URL            FlexString           `json:"url"`
		Home           FlexString           `json:"home"`
		GMTOffset      FlexFloat64          `json:"gmt_offset"`
		TimezoneString FlexString           `json:"timezone_string"`
		Namespaces     FlexStringSlice      `json:"namespaces"`
		Authentication FlexMap[any]         `json:"authentication"`
		Routes         FlexMap[RouteSchema] `json:"routes"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	sp.Name = string(wire.Name)
	sp.Description = string(wire.Description)
	sp.URL = string(wire.URL)
	sp.Home = string(wire.Home)
	sp.GMTOffset = float64(wire.GMTOffset)
	sp.TimezoneString = string(wire.TimezoneString)
	sp.Namespaces = wire.Namespaces
	sp.Authentication = wire.Authentication
	sp.Routes = wire.Routes
	return nil
}

// RouteSchema describes a single WP REST API route entry in `/wp-json/`.
type RouteSchema struct {
	Namespace string   `json:"namespace"`
	Methods   []string `json:"methods"`
}

func (rs *RouteSchema) UnmarshalJSON(data []byte) error {
	var wire struct {
		Namespace FlexString      `json:"namespace"`
		Methods   FlexStringSlice `json:"methods"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return nil
	}
	rs.Namespace = string(wire.Namespace)
	rs.Methods = wire.Methods
	return nil
}

// PostType represents `/wp/v2/types` items (posts, pages, attachment, product, etc.).
type PostType struct {
	Name          string   `json:"name" yaml:"name"`
	Slug          string   `json:"slug" yaml:"slug"`
	Description   string   `json:"description" yaml:"description"`
	RESTBase      string   `json:"rest_base" yaml:"rest_base"`
	RESTNamespace string   `json:"rest_namespace" yaml:"rest_namespace"`
	Hierarchical  bool     `json:"hierarchical" yaml:"hierarchical"`
	Taxonomies    []string `json:"taxonomies" yaml:"taxonomies"`
}

func (pt *PostType) UnmarshalJSON(data []byte) error {
	var wire struct {
		Name          FlexString      `json:"name"`
		Slug          FlexString      `json:"slug"`
		Description   FlexString      `json:"description"`
		RESTBase      FlexString      `json:"rest_base"`
		RESTNamespace FlexString      `json:"rest_namespace"`
		Hierarchical  FlexBool        `json:"hierarchical"`
		Taxonomies    FlexStringSlice `json:"taxonomies"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return nil
	}
	pt.Name = string(wire.Name)
	pt.Slug = string(wire.Slug)
	pt.Description = string(wire.Description)
	pt.RESTBase = string(wire.RESTBase)
	pt.RESTNamespace = string(wire.RESTNamespace)
	pt.Hierarchical = bool(wire.Hierarchical)
	pt.Taxonomies = wire.Taxonomies
	return nil
}

// Taxonomy represents `/wp/v2/taxonomies` items (category, post_tag, product_cat, etc.).
type Taxonomy struct {
	Name          string   `json:"name" yaml:"name"`
	Slug          string   `json:"slug" yaml:"slug"`
	Description   string   `json:"description" yaml:"description"`
	RESTBase      string   `json:"rest_base" yaml:"rest_base"`
	RESTNamespace string   `json:"rest_namespace" yaml:"rest_namespace"`
	Hierarchical  bool     `json:"hierarchical" yaml:"hierarchical"`
	Types         []string `json:"types" yaml:"types"`
}

func (tx *Taxonomy) UnmarshalJSON(data []byte) error {
	var wire struct {
		Name          FlexString      `json:"name"`
		Slug          FlexString      `json:"slug"`
		Description   FlexString      `json:"description"`
		RESTBase      FlexString      `json:"rest_base"`
		RESTNamespace FlexString      `json:"rest_namespace"`
		Hierarchical  FlexBool        `json:"hierarchical"`
		Types         FlexStringSlice `json:"types"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return nil
	}
	tx.Name = string(wire.Name)
	tx.Slug = string(wire.Slug)
	tx.Description = string(wire.Description)
	tx.RESTBase = string(wire.RESTBase)
	tx.RESTNamespace = string(wire.RESTNamespace)
	tx.Hierarchical = bool(wire.Hierarchical)
	tx.Types = wire.Types
	return nil
}

// Post represents a WordPress post, page, or Custom Post Type item.
type Post struct {
	ID            int            `json:"id" yaml:"id"`
	Date          WPTime         `json:"date" yaml:"date"`
	DateGMT       WPTime         `json:"date_gmt" yaml:"date_gmt"`
	Modified      WPTime         `json:"modified" yaml:"modified"`
	ModifiedGMT   WPTime         `json:"modified_gmt" yaml:"modified_gmt"`
	Slug          string         `json:"slug" yaml:"slug"`
	Status        string         `json:"status" yaml:"status"`
	Type          string         `json:"type" yaml:"type"`
	Link          string         `json:"link" yaml:"link"`
	GUID          RenderedField  `json:"guid" yaml:"guid"`
	Title         RenderedField  `json:"title" yaml:"title"`
	Content       RenderedField  `json:"content" yaml:"content"`
	Excerpt       RenderedField  `json:"excerpt" yaml:"excerpt"`
	Author        int            `json:"author" yaml:"author"`
	FeaturedMedia int            `json:"featured_media" yaml:"featured_media"`
	CommentStatus string         `json:"comment_status" yaml:"comment_status"`
	PingStatus    string         `json:"ping_status" yaml:"ping_status"`
	Sticky        bool           `json:"sticky" yaml:"sticky"`
	Template      string         `json:"template" yaml:"template"`
	Format        string         `json:"format" yaml:"format"`
	Categories    []int          `json:"categories,omitempty" yaml:"categories,omitempty"`
	Tags          []int          `json:"tags,omitempty" yaml:"tags,omitempty"`
	Embedded      *EmbeddedData  `json:"_embedded,omitempty" yaml:"-"`
	Extra         map[string]any `json:"extra,omitempty" yaml:"extra,omitempty"`
}

func (p *Post) UnmarshalJSON(data []byte) error {
	var wire struct {
		ID            FlexInt       `json:"id"`
		Date          WPTime        `json:"date"`
		DateGMT       WPTime        `json:"date_gmt"`
		Modified      WPTime        `json:"modified"`
		ModifiedGMT   WPTime        `json:"modified_gmt"`
		Slug          FlexString    `json:"slug"`
		Status        FlexString    `json:"status"`
		Type          FlexString    `json:"type"`
		Link          FlexString    `json:"link"`
		GUID          RenderedField `json:"guid"`
		Title         RenderedField `json:"title"`
		Content       RenderedField `json:"content"`
		Excerpt       RenderedField `json:"excerpt"`
		Author        FlexInt       `json:"author"`
		FeaturedMedia FlexInt       `json:"featured_media"`
		CommentStatus FlexString    `json:"comment_status"`
		PingStatus    FlexString    `json:"ping_status"`
		Sticky        FlexBool      `json:"sticky"`
		Template      FlexString    `json:"template"`
		Format        FlexString    `json:"format"`
		Categories    FlexIntSlice  `json:"categories"`
		Tags          FlexIntSlice  `json:"tags"`
		Embedded      *EmbeddedData `json:"_embedded"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	p.ID = int(wire.ID)
	p.Date = wire.Date
	p.DateGMT = wire.DateGMT
	p.Modified = wire.Modified
	p.ModifiedGMT = wire.ModifiedGMT
	p.Slug = string(wire.Slug)
	p.Status = string(wire.Status)
	p.Type = string(wire.Type)
	p.Link = string(wire.Link)
	p.GUID = wire.GUID
	p.Title = wire.Title
	p.Content = wire.Content
	p.Excerpt = wire.Excerpt
	p.Author = int(wire.Author)
	p.FeaturedMedia = int(wire.FeaturedMedia)
	p.CommentStatus = string(wire.CommentStatus)
	p.PingStatus = string(wire.PingStatus)
	p.Sticky = bool(wire.Sticky)
	p.Template = string(wire.Template)
	p.Format = string(wire.Format)
	p.Categories = wire.Categories
	p.Tags = wire.Tags
	p.Embedded = wire.Embedded
	return nil
}

// EmbeddedData holds `_embed=1` resolved relations on a Post.
type EmbeddedData struct {
	Author        FlexSlice[User]             `json:"author,omitempty"`
	FeaturedMedia FlexSlice[MediaItem]        `json:"wp:featuredmedia,omitempty"`
	WpTerm        FlexSlice[FlexSlice[TermItem]] `json:"wp:term,omitempty"`
	Replies       FlexSlice[FlexSlice[Comment]]  `json:"replies,omitempty"`
}

// ResolveAuthorName returns the embedded author name or a fallback ID label.
func (p Post) ResolveAuthorName() string {
	if p.Embedded != nil && len(p.Embedded.Author) > 0 && p.Embedded.Author[0].Name != "" {
		return p.Embedded.Author[0].Name
	}
	if p.Author > 0 {
		return fmt.Sprintf("user#%d", p.Author)
	}
	return "—"
}

// ResolveFeaturedURL returns the embedded featured image source URL if present.
func (p Post) ResolveFeaturedURL() string {
	if p.Embedded != nil && len(p.Embedded.FeaturedMedia) > 0 {
		return p.Embedded.FeaturedMedia[0].SourceURL
	}
	return ""
}

// ResolveTermNames returns embedded term names for a given taxonomy (e.g. "category" or "post_tag").
func (p Post) ResolveTermNames(taxonomy string) []string {
	var names []string
	if p.Embedded == nil {
		return names
	}
	for _, group := range p.Embedded.WpTerm {
		for _, term := range group {
			if taxonomy == "" || term.Taxonomy == taxonomy {
				if term.Name != "" {
					names = append(names, term.Name)
				}
			}
		}
	}
	return names
}

// MediaItem represents `/wp/v2/media` attachments.
type MediaItem struct {
	ID           int           `json:"id" yaml:"id"`
	Date         WPTime        `json:"date" yaml:"date"`
	DateGMT      WPTime        `json:"date_gmt" yaml:"date_gmt"`
	Modified     WPTime        `json:"modified" yaml:"modified"`
	Slug         string        `json:"slug" yaml:"slug"`
	Status       string        `json:"status" yaml:"status"`
	Type         string        `json:"type" yaml:"type"`
	Link         string        `json:"link" yaml:"link"`
	Title        RenderedField `json:"title" yaml:"title"`
	Author       int           `json:"author" yaml:"author"`
	Caption      RenderedField `json:"caption" yaml:"caption"`
	AltText      string        `json:"alt_text" yaml:"alt_text"`
	MediaType    string        `json:"media_type" yaml:"media_type"`
	MimeType     string        `json:"mime_type" yaml:"mime_type"`
	PostID       int           `json:"post" yaml:"post"`
	SourceURL    string        `json:"source_url" yaml:"source_url"`
	MediaDetails MediaDetails  `json:"media_details" yaml:"media_details"`
}

func (m *MediaItem) UnmarshalJSON(data []byte) error {
	var wire struct {
		ID           FlexInt       `json:"id"`
		Date         WPTime        `json:"date"`
		DateGMT      WPTime        `json:"date_gmt"`
		Modified     WPTime        `json:"modified"`
		Slug         FlexString    `json:"slug"`
		Status       FlexString    `json:"status"`
		Type         FlexString    `json:"type"`
		Link         FlexString    `json:"link"`
		Title        RenderedField `json:"title"`
		Author       FlexInt       `json:"author"`
		Caption      RenderedField `json:"caption"`
		AltText      FlexString    `json:"alt_text"`
		MediaType    FlexString    `json:"media_type"`
		MimeType     FlexString    `json:"mime_type"`
		PostID       FlexInt       `json:"post"`
		SourceURL    FlexString    `json:"source_url"`
		MediaDetails MediaDetails  `json:"media_details"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	m.ID = int(wire.ID)
	m.Date = wire.Date
	m.DateGMT = wire.DateGMT
	m.Modified = wire.Modified
	m.Slug = string(wire.Slug)
	m.Status = string(wire.Status)
	m.Type = string(wire.Type)
	m.Link = string(wire.Link)
	m.Title = wire.Title
	m.Author = int(wire.Author)
	m.Caption = wire.Caption
	m.AltText = string(wire.AltText)
	m.MediaType = string(wire.MediaType)
	m.MimeType = string(wire.MimeType)
	m.PostID = int(wire.PostID)
	m.SourceURL = string(wire.SourceURL)
	m.MediaDetails = wire.MediaDetails
	return nil
}

type MediaDetails struct {
	Width    int                       `json:"width" yaml:"width"`
	Height   int                       `json:"height" yaml:"height"`
	File     string                    `json:"file" yaml:"file"`
	FileSize int64                     `json:"filesize,omitempty" yaml:"filesize,omitempty"`
	Sizes    map[string]MediaSizeEntry `json:"sizes,omitempty" yaml:"sizes,omitempty"`
}

// UnmarshalJSON handles PHP's dynamic typing on `media_details` (which can be `[]`, `false`, `null`,
// or an object where `width`, `height`, `filesize`, or `sizes` have mixed PHP types).
func (md *MediaDetails) UnmarshalJSON(data []byte) error {
	s := strings.TrimSpace(string(data))
	if len(s) == 0 || s[0] != '{' {
		return nil
	}
	var wire struct {
		Width    FlexInt                 `json:"width"`
		Height   FlexInt                 `json:"height"`
		File     FlexString              `json:"file"`
		FileSize FlexInt64               `json:"filesize"`
		Sizes    FlexMap[MediaSizeEntry] `json:"sizes"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return nil
	}
	md.Width = int(wire.Width)
	md.Height = int(wire.Height)
	md.File = string(wire.File)
	md.FileSize = int64(wire.FileSize)
	md.Sizes = wire.Sizes
	return nil
}

type MediaSizeEntry struct {
	File      string `json:"file" yaml:"file"`
	Width     int    `json:"width" yaml:"width"`
	Height    int    `json:"height" yaml:"height"`
	MimeType  string `json:"mime_type" yaml:"mime_type"`
	SourceURL string `json:"source_url" yaml:"source_url"`
}

func (mse *MediaSizeEntry) UnmarshalJSON(data []byte) error {
	s := strings.TrimSpace(string(data))
	if len(s) == 0 || s[0] != '{' {
		return nil
	}
	var wire struct {
		File      FlexString `json:"file"`
		Width     FlexInt    `json:"width"`
		Height    FlexInt    `json:"height"`
		MimeType  FlexString `json:"mime_type"`
		SourceURL FlexString `json:"source_url"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return nil
	}
	mse.File = string(wire.File)
	mse.Width = int(wire.Width)
	mse.Height = int(wire.Height)
	mse.MimeType = string(wire.MimeType)
	mse.SourceURL = string(wire.SourceURL)
	return nil
}

func (m MediaItem) DimensionsLabel() string {
	if m.MediaDetails.Width > 0 && m.MediaDetails.Height > 0 {
		return fmt.Sprintf("%dx%d", m.MediaDetails.Width, m.MediaDetails.Height)
	}
	return "—"
}

// TermItem represents a category, tag, or custom taxonomy term (`/wp/v2/categories`, `/wp/v2/tags`, etc.).
type TermItem struct {
	ID          int    `json:"id" yaml:"id"`
	Count       int    `json:"count" yaml:"count"`
	Description string `json:"description" yaml:"description"`
	Link        string `json:"link" yaml:"link"`
	Name        string `json:"name" yaml:"name"`
	Slug        string `json:"slug" yaml:"slug"`
	Taxonomy    string `json:"taxonomy" yaml:"taxonomy"`
	Parent      int    `json:"parent,omitempty" yaml:"parent,omitempty"`
}

func (t *TermItem) UnmarshalJSON(data []byte) error {
	var wire struct {
		ID          FlexInt    `json:"id"`
		Count       FlexInt    `json:"count"`
		Description FlexString `json:"description"`
		Link        FlexString `json:"link"`
		Name        FlexString `json:"name"`
		Slug        FlexString `json:"slug"`
		Taxonomy    FlexString `json:"taxonomy"`
		Parent      FlexInt    `json:"parent"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	t.ID = int(wire.ID)
	t.Count = int(wire.Count)
	t.Description = string(wire.Description)
	t.Link = string(wire.Link)
	t.Name = string(wire.Name)
	t.Slug = string(wire.Slug)
	t.Taxonomy = string(wire.Taxonomy)
	t.Parent = int(wire.Parent)
	return nil
}

// User represents `/wp/v2/users` public author profiles.
type User struct {
	ID          int               `json:"id" yaml:"id"`
	Name        string            `json:"name" yaml:"name"`
	URL         string            `json:"url" yaml:"url"`
	Description string            `json:"description" yaml:"description"`
	Link        string            `json:"link" yaml:"link"`
	Slug        string            `json:"slug" yaml:"slug"`
	AvatarURLs  map[string]string `json:"avatar_urls,omitempty" yaml:"avatar_urls,omitempty"`
}

func (u *User) UnmarshalJSON(data []byte) error {
	var wire struct {
		ID          FlexInt             `json:"id"`
		Name        FlexString          `json:"name"`
		URL         FlexString          `json:"url"`
		Description FlexString          `json:"description"`
		Link        FlexString          `json:"link"`
		Slug        FlexString          `json:"slug"`
		AvatarURLs  FlexMap[FlexString] `json:"avatar_urls"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	u.ID = int(wire.ID)
	u.Name = string(wire.Name)
	u.URL = string(wire.URL)
	u.Description = string(wire.Description)
	u.Link = string(wire.Link)
	u.Slug = string(wire.Slug)
	if len(wire.AvatarURLs) > 0 {
		u.AvatarURLs = make(map[string]string, len(wire.AvatarURLs))
		for k, v := range wire.AvatarURLs {
			u.AvatarURLs[k] = string(v)
		}
	}
	return nil
}

// Comment represents `/wp/v2/comments` public comments.
type Comment struct {
	ID         int           `json:"id" yaml:"id"`
	Post       int           `json:"post" yaml:"post"`
	Parent     int           `json:"parent" yaml:"parent"`
	Author     int           `json:"author" yaml:"author"`
	AuthorName string        `json:"author_name" yaml:"author_name"`
	AuthorURL  string        `json:"author_url" yaml:"author_url"`
	Date       WPTime        `json:"date" yaml:"date"`
	DateGMT    WPTime        `json:"date_gmt" yaml:"date_gmt"`
	Content    RenderedField `json:"content" yaml:"content"`
	Link       string        `json:"link" yaml:"link"`
	Status     string        `json:"status" yaml:"status"`
	Type       string        `json:"type" yaml:"type"`
}

func (c *Comment) UnmarshalJSON(data []byte) error {
	var wire struct {
		ID         FlexInt       `json:"id"`
		Post       FlexInt       `json:"post"`
		Parent     FlexInt       `json:"parent"`
		Author     FlexInt       `json:"author"`
		AuthorName FlexString    `json:"author_name"`
		AuthorURL  FlexString    `json:"author_url"`
		Date       WPTime        `json:"date"`
		DateGMT    WPTime        `json:"date_gmt"`
		Content    RenderedField `json:"content"`
		Link       FlexString    `json:"link"`
		Status     FlexString    `json:"status"`
		Type       FlexString    `json:"type"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	c.ID = int(wire.ID)
	c.Post = int(wire.Post)
	c.Parent = int(wire.Parent)
	c.Author = int(wire.Author)
	c.AuthorName = string(wire.AuthorName)
	c.AuthorURL = string(wire.AuthorURL)
	c.Date = wire.Date
	c.DateGMT = wire.DateGMT
	c.Content = wire.Content
	c.Link = string(wire.Link)
	c.Status = string(wire.Status)
	c.Type = string(wire.Type)
	return nil
}

// QueryFilter encapsulates all WordPress REST API query parameters for searching, filtering, and sorting.
type QueryFilter struct {
	Endpoint   string // e.g., "posts", "pages", "media", "categories", "tags", "users", "comments", or CPT rest_base
	Search     string
	Author     []int
	Categories []int
	Tags       []int
	Status     string // default "publish"
	After      string // ISO-8601 date string
	Before     string // ISO-8601 date string
	OrderBy    string // date, modified, title, id, slug, relevance, count, name
	Order      string // desc, asc
	PerPage    int    // 1..100, default 25 in interactive, 100 in batch
	Page       int    // 1-indexed
	Offset     int
	Sticky            *bool
	MimeType          string   // for media: image, video, audio, application/pdf, or comma-separated list/presets
	MediaType         string   // image, file, video, audio, text, application (can be comma-separated)
	Extensions        []string // file extension or preset filter (e.g., zip, csv, xls, xlsx, excel, archive)
	ExcludeMediaTypes []string // media_type or MIME prefix to exclude (e.g., image, video, audio)
	ExcludeExtensions []string // file extensions or presets to exclude (e.g., jpg, png, webp, zip)
	Parent            *int
	Slug       string
	Fields     []string // _fields projection
	Embed      bool     // _embed=1
	MaxPages   int      // 0 = unlimited when paginating all
	MaxItems   int      // 0 = unlimited
}

func DefaultQueryFilter(endpoint string) QueryFilter {
	orderBy := "date"
	order := "desc"
	switch endpoint {
	case "categories", "tags":
		orderBy = "count"
		order = "desc"
	case "users":
		orderBy = "name"
		order = "asc"
	}
	return QueryFilter{
		Endpoint: endpoint,
		OrderBy:  orderBy,
		Order:    order,
		PerPage:  25,
		Page:     1,
		Embed:    endpoint == "posts" || endpoint == "pages",
	}
}

// PageMeta holds pagination headers returned by WordPress REST API (`X-WP-Total`, `X-WP-TotalPages`).
type PageMeta struct {
	TotalItems int           `json:"total_items"`
	TotalPages int           `json:"total_pages"`
	Page       int           `json:"page"`
	PerPage    int           `json:"per_page"`
	Latency    time.Duration `json:"latency_ms"`
	RequestURL string        `json:"request_url"`
}
