// The built-in sensitive-data detectors, in the order the rule editor offers
// them. Mirrors internal/emailfilter/detect.go; an unknown name is refused by
// the server with invalid_argument, so this is about offering the right ones.
export const DETECTORS: { value: string; label: string }[] = [
  { value: 'credit_card', label: 'Card number (Luhn checked)' },
  { value: 'id_nik', label: 'NIK (Indonesian ID number)' },
  { value: 'id_npwp', label: 'NPWP (Indonesian tax number)' },
  { value: 'iban', label: 'IBAN' },
  { value: 'email_address', label: 'Email address in content' },
  { value: 'phone_number', label: 'Phone number' },
  { value: 'secret', label: 'API key or token' },
  { value: 'private_key', label: 'Private key' },
];

export function detectorLabel(name: string): string {
  return DETECTORS.find((d) => d.value === name)?.label ?? name.replace(/_/g, ' ');
}

// Where a finding was seen, in words: "attachment:payroll.xlsx" reads as
// "payroll.xlsx", "attachment_name:x" as "the name of x".
export function describeLocation(location: string): string {
  if (location.startsWith('attachment:')) return location.slice('attachment:'.length);
  if (location.startsWith('attachment_name:')) return `name of ${location.slice('attachment_name:'.length)}`;
  return location;
}
