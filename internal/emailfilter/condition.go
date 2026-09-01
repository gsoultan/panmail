package emailfilter

import (
	"fmt"
	"regexp"
	"strings"
)

// Field is what a condition looks at.
type Field string

const (
	FieldFrom         Field = "from"
	FieldTo           Field = "to"
	FieldCc           Field = "cc"
	FieldBcc          Field = "bcc"
	FieldAnyRecipient Field = "any_recipient"

	FieldSubject       Field = "subject"
	FieldBody          Field = "body"
	FieldSubjectOrBody Field = "subject_or_body"
	FieldHeader        Field = "header"

	FieldAttachmentName      Field = "attachment_name"
	FieldAttachmentExtension Field = "attachment_extension"

	FieldAttachmentCount     Field = "attachment_count"
	FieldAttachmentSize      Field = "attachment_size"
	FieldTotalAttachmentSize Field = "total_attachment_size"
	FieldMessageSize         Field = "message_size"
	FieldRecipientCount      Field = "recipient_count"

	FieldHasAttachment           Field = "has_attachment"
	FieldHasExecutableAttachment Field = "has_executable_attachment"

	FieldProvider Field = "provider"

	FieldSPF   Field = "spf"
	FieldDKIM  Field = "dkim"
	FieldDMARC Field = "dmarc"
)

// Operator is how the field is compared.
type Operator string

const (
	// OpContains is a case-insensitive substring match.
	//
	// Substring rather than whole-word, which is what Exchange does. Whole-word
	// is defensible for prose but astonishing for addresses: with word matching
	// "contoso" does not match "acontoso", and people write rules about
	// "invoice" expecting "invoices" to match.
	OpContains Operator = "contains"

	// OpMatches is a regular expression, case-insensitive.
	//
	// Go's regexp is RE2, so a pathological pattern costs linear time rather
	// than hanging the send path. That is the reason this is safe to expose to
	// a tenant at all.
	OpMatches Operator = "matches"

	// OpEquals is a case-insensitive whole-value match.
	OpEquals Operator = "equals"

	// OpDomainIs matches the domain of an address, and its subdomains, so
	// "example.com" also matches "mail.example.com".
	OpDomainIs Operator = "domain_is"

	// OpIn is a case-insensitive membership test, for extension lists.
	OpIn Operator = "in"

	// OpGreaterOrEqual compares a numeric field against Number.
	OpGreaterOrEqual Operator = "gte"

	// OpIsTrue asserts a boolean field.
	OpIsTrue Operator = "is_true"

	// OpExists asserts a header is present, whatever its value.
	OpExists Operator = "exists"
)

type fieldKind int

const (
	kindAddress fieldKind = iota
	kindText
	kindHeader
	kindNumber
	kindBool
	kindEnum
)

var fieldKinds = map[Field]fieldKind{
	FieldFrom: kindAddress, FieldTo: kindAddress, FieldCc: kindAddress,
	FieldBcc: kindAddress, FieldAnyRecipient: kindAddress,

	FieldSubject: kindText, FieldBody: kindText, FieldSubjectOrBody: kindText,
	FieldAttachmentName: kindText, FieldAttachmentExtension: kindText,

	FieldHeader: kindHeader,

	FieldAttachmentCount: kindNumber, FieldAttachmentSize: kindNumber,
	FieldTotalAttachmentSize: kindNumber, FieldMessageSize: kindNumber,
	FieldRecipientCount: kindNumber,

	FieldHasAttachment: kindBool, FieldHasExecutableAttachment: kindBool,

	FieldProvider: kindEnum, FieldSPF: kindEnum, FieldDKIM: kindEnum, FieldDMARC: kindEnum,
}

var allowedOperators = map[fieldKind]map[Operator]bool{
	kindAddress: {OpContains: true, OpMatches: true, OpEquals: true, OpDomainIs: true},
	kindText:    {OpContains: true, OpMatches: true, OpEquals: true, OpIn: true},
	kindHeader:  {OpContains: true, OpMatches: true, OpEquals: true, OpExists: true},
	kindNumber:  {OpGreaterOrEqual: true},
	kindBool:    {OpIsTrue: true},
	kindEnum:    {OpEquals: true, OpIn: true},
}

// inboundOnlyFields cannot be evaluated on the way out, because nothing has
// authenticated the message yet.
var inboundOnlyFields = map[Field]bool{FieldSPF: true, FieldDKIM: true, FieldDMARC: true}

// outboundOnlyFields have no meaning inbound: a message arriving has not been
// assigned one of this tenant's providers.
var outboundOnlyFields = map[Field]bool{FieldProvider: true}

// Condition is one test. Values are ORed against each other, so a single
// condition covers "the subject contains invoice or receipt"; separate
// conditions are ANDed by the rule.
type Condition struct {
	Field    Field    `json:"field"`
	Operator Operator `json:"operator"`
	Values   []string `json:"values,omitempty"`

	// Header is the header name, when Field is FieldHeader.
	Header string `json:"header,omitempty"`

	// Number is the threshold, for the numeric fields. Sizes are in bytes.
	Number int64 `json:"number,omitempty"`

	// compiled is populated by Compile and is what OpMatches uses. Compiling
	// at evaluation time would put a regexp compile on the send path for every
	// message, and would turn a bad pattern into a runtime error in the middle
	// of a send rather than a validation error when the rule was saved.
	compiled []*regexp.Regexp
}

// Validate reports whether the condition is well formed, and compiles any
// patterns. Call it before storing a rule, not while sending.
func (c *Condition) Validate(direction Direction) error {
	kind, ok := fieldKinds[c.Field]
	if !ok {
		return fmt.Errorf("emailfilter: unknown field %q", c.Field)
	}
	if inboundOnlyFields[c.Field] && direction != DirectionInbound {
		return fmt.Errorf("emailfilter: %s is only available on inbound rules", c.Field)
	}
	if outboundOnlyFields[c.Field] && direction != DirectionOutbound {
		return fmt.Errorf("emailfilter: %s is only available on outbound rules", c.Field)
	}
	if !allowedOperators[kind][c.Operator] {
		return fmt.Errorf("emailfilter: operator %q cannot be used with field %q", c.Operator, c.Field)
	}
	if c.Field == FieldHeader && strings.TrimSpace(c.Header) == "" {
		return fmt.Errorf("emailfilter: a header condition needs a header name")
	}

	needsValues := c.Operator != OpIsTrue && c.Operator != OpExists && c.Operator != OpGreaterOrEqual
	if needsValues && len(c.Values) == 0 {
		return fmt.Errorf("emailfilter: %s %s needs at least one value", c.Field, c.Operator)
	}
	if c.Operator == OpGreaterOrEqual && c.Number < 0 {
		return fmt.Errorf("emailfilter: %s gte needs a threshold of zero or more", c.Field)
	}

	c.compiled = nil
	if c.Operator == OpMatches {
		for _, value := range c.Values {
			// Anchored nowhere on purpose: a pattern is a search, and a caller
			// who wants the whole value can write ^...$.
			re, err := regexp.Compile("(?i)" + value)
			if err != nil {
				return fmt.Errorf("emailfilter: %s is not a valid pattern: %w", value, err)
			}
			c.compiled = append(c.compiled, re)
		}
	}
	return nil
}

// Matches reports whether the message satisfies the condition.
func (c Condition) Matches(m Message) bool {
	switch c.Field {
	case FieldHasAttachment:
		return len(m.Attachments) > 0
	case FieldHasExecutableAttachment:
		for _, a := range m.Attachments {
			if a.IsExecutable() {
				return true
			}
		}
		return false
	}

	switch fieldKinds[c.Field] {
	case kindNumber:
		return c.number(m) >= c.Number
	case kindHeader:
		values := m.Header(c.Header)
		if c.Operator == OpExists {
			return len(values) > 0
		}
		return c.anyValueMatches(values)
	default:
		return c.anyValueMatches(c.subjects(m))
	}
}

// number pulls the numeric field out of the message.
func (c Condition) number(m Message) int64 {
	switch c.Field {
	case FieldAttachmentCount:
		return int64(len(m.Attachments))
	case FieldAttachmentSize:
		return m.LargestAttachment()
	case FieldTotalAttachmentSize:
		return m.TotalAttachmentSize()
	case FieldMessageSize:
		return m.Size
	case FieldRecipientCount:
		return int64(len(m.Recipients()))
	}
	return 0
}

// subjects is the list of strings the condition tests against. A condition on
// attachments tests every attachment, so one matching file matches the rule.
func (c Condition) subjects(m Message) []string {
	switch c.Field {
	case FieldFrom:
		return []string{m.From}
	case FieldTo:
		return m.To
	case FieldCc:
		return m.Cc
	case FieldBcc:
		return m.Bcc
	case FieldAnyRecipient:
		return m.Recipients()
	case FieldSubject:
		return []string{m.Subject}
	case FieldBody:
		return []string{m.HTML, m.Text}
	case FieldSubjectOrBody:
		return []string{m.Subject, m.HTML, m.Text}
	case FieldAttachmentName:
		names := make([]string, 0, len(m.Attachments))
		for _, a := range m.Attachments {
			names = append(names, a.Filename)
		}
		return names
	case FieldAttachmentExtension:
		exts := make([]string, 0, len(m.Attachments))
		for _, a := range m.Attachments {
			exts = append(exts, a.Extension())
		}
		return exts
	case FieldProvider:
		return []string{m.ProviderID}
	case FieldSPF:
		return []string{string(m.SPF)}
	case FieldDKIM:
		return []string{string(m.DKIM)}
	case FieldDMARC:
		return []string{string(m.DMARC)}
	}
	return nil
}

// anyValueMatches is the OR across a condition's own values, and across the
// several strings a field can produce — one recipient matching is the field
// matching.
func (c Condition) anyValueMatches(subjects []string) bool {
	for _, subject := range subjects {
		if subject == "" {
			continue
		}
		if c.Operator == OpMatches {
			for _, re := range c.compiled {
				if re.MatchString(subject) {
					return true
				}
			}
			continue
		}

		lowered := strings.ToLower(subject)
		for _, value := range c.Values {
			wanted := strings.ToLower(strings.TrimSpace(value))
			if wanted == "" {
				continue
			}
			switch c.Operator {
			case OpContains:
				if strings.Contains(lowered, wanted) {
					return true
				}
			case OpEquals, OpIn:
				if lowered == wanted {
					return true
				}
			case OpDomainIs:
				if domainMatches(lowered, wanted) {
					return true
				}
			}
		}
	}
	return false
}

// domainMatches is true for the domain itself and for any subdomain of it, so
// a rule about example.com also catches mail.example.com. Without the dot,
// "example.com" would also match "notexample.com".
func domainMatches(address, domain string) bool {
	at := strings.LastIndex(address, "@")
	if at < 0 {
		return false
	}
	host := strings.Trim(address[at+1:], "> ")
	return host == domain || strings.HasSuffix(host, "."+domain)
}
