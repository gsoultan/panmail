-- Verification material for a provider's delivery webhooks. Encrypted at rest.
ALTER TABLE email_providers ADD COLUMN webhook_secret TEXT;
