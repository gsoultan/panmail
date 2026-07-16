package usecases

import (
	"fmt"

	"github.com/gsoultan/gsmail/imap"
	"github.com/gsoultan/gsmail/pop3"
	"github.com/gsoultan/gsmail/smtp"
	panmailv1 "github.com/gsoultan/panmail/api/panmail/v1"
	"github.com/gsoultan/panmail/internal/email_provider/repositories/entities"
	"google.golang.org/protobuf/encoding/protojson"
)

type providerFactory struct{}

func NewProviderFactory() entities.ProviderFactory {
	return &providerFactory{}
}

func (f *providerFactory) CreateSender(p *entities.EmailProvider) (any, error) {
	switch p.Type {
	case panmailv1.ProviderType_PROVIDER_TYPE_SMTP:
		c := &panmailv1.SmtpConfig{}
		if err := protojson.Unmarshal(p.Config, c); err != nil {
			return nil, err
		}
		s := smtp.NewSender(c.Host, int(c.Port), c.Username, c.Password, c.UseSsl)
		s.InsecureSkipVerify = c.SkipVerify
		return s, nil
	default:
		return nil, fmt.Errorf("provider type %v does not support sending", p.Type)
	}
}

func (f *providerFactory) CreateReceiver(p *entities.EmailProvider) (any, error) {
	switch p.Type {
	case panmailv1.ProviderType_PROVIDER_TYPE_IMAP:
		c := &panmailv1.ImapConfig{}
		if err := protojson.Unmarshal(p.Config, c); err != nil {
			return nil, err
		}
		r := imap.NewReceiver(c.Host, int(c.Port), c.Username, c.Password, c.UseSsl)
		r.InsecureSkipVerify = c.SkipVerify
		return r, nil
	case panmailv1.ProviderType_PROVIDER_TYPE_POP3:
		c := &panmailv1.Pop3Config{}
		if err := protojson.Unmarshal(p.Config, c); err != nil {
			return nil, err
		}
		r := pop3.NewReceiver(c.Host, int(c.Port), c.Username, c.Password, c.UseSsl)
		r.InsecureSkipVerify = c.SkipVerify
		return r, nil
	default:
		return nil, fmt.Errorf("provider type %v does not support receiving", p.Type)
	}
}
