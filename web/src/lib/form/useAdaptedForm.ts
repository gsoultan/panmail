import { useCallback, useEffect, useMemo, useState } from 'react';
import { useForm, useStore } from '@tanstack/react-form';

/**
 * TanStack Form behind the field API the Mantine inputs already speak.
 *
 * The forms in this app pass a `form` object down to section components, which
 * spread `form.getInputProps(path)` onto Mantine inputs. Swapping the library
 * outright would mean rewriting every field in every section as a render-prop
 * `<form.Field>` — several hundred lines of JSX across files that carry the
 * provider features, with no component tests to catch a mistyped path. The
 * adapter keeps that JSX untouched while the state underneath it becomes
 * TanStack's, so sections can move to the native API one at a time.
 *
 * What TanStack buys here is per-field async validation — the thing a provider
 * form actually wants, since "is this host reachable" can only be answered by
 * the server. Mantine's resolver is synchronous, which is why testing a
 * connection is a separate button rather than part of validating the form.
 */

export type FieldErrors = Record<string, string | undefined>;

/** Validates the whole value set, returning errors keyed by field path. */
export type Validate<T> = (values: T) => FieldErrors;

/**
 * A rule per field, which is the shape Mantine's resolver took.
 *
 * Accepted as well as the whole-form function so a form can move across by
 * changing its import rather than restating every rule — twenty of them across
 * the app, each an opportunity to change a condition while retyping it.
 */
export type FieldValidators = Record<string, (value: any, values: any) => string | null | undefined>;

const toValidate = <T,>(validate: Validate<T> | FieldValidators | undefined): Validate<T> | undefined => {
  if (!validate) return undefined;
  if (typeof validate === 'function') return validate;

  return (values: T) => {
    const errors: FieldErrors = {};
    for (const [path, rule] of Object.entries(validate)) {
      errors[path] = rule(readPath(values, path), values) ?? undefined;
    }
    return errors;
  };
};

/**
 * Validates against something only the server can answer.
 *
 * Kept separate from the synchronous validator rather than merged into it,
 * because the two have to behave differently: a synchronous rule reruns on
 * every keystroke and is free, while this one costs a request and must be
 * debounced, cancelled when the values move on, and must never block a submit
 * on an answer that has not arrived.
 */
export type ValidateAsync<T> = (values: T, signal: AbortSignal) => Promise<FieldErrors>;

interface AdaptedFormOptions<T> {
  initialValues: T;
  validate?: Validate<T> | FieldValidators;
  validateAsync?: ValidateAsync<T>;
  /** How long the values must be still before the async check runs. */
  asyncDebounceMs?: number;
  /**
   * Optional, because a form may instead pass its handler at the point of
   * submission — `form.onSubmit(handler)` — which is how most of these were
   * written.
   */
  onSubmit?: (values: T) => void | Promise<void>;
}

/**
 * Long enough that it does not fire mid-word, short enough that the answer
 * arrives while the field is still what the author is looking at.
 */
const DEFAULT_ASYNC_DEBOUNCE = 700;

/** Reads "smtp.dkim.domain" out of a nested object. */
const readPath = (source: unknown, path: string): unknown => {
  let current = source;
  for (const part of path.split('.')) {
    if (current === null || typeof current !== 'object') return undefined;
    current = (current as Record<string, unknown>)[part];
  }
  return current;
};

export interface AdaptedForm<T> {
  values: T;
  errors: FieldErrors;
  /** True while an async check is in flight, for a spinner on the field. */
  isValidating: boolean;
  setFieldValue: (path: string, value: unknown) => void;
  setValues: (values: Partial<T>) => void;
  /** Returns every field to the values the form was built with. */
  reset: () => void;
  insertListItem: (path: string, item: unknown, index?: number) => void;
  removeListItem: (path: string, index: number) => void;
  getInputProps: (path: string, options?: { type?: 'checkbox' | 'input' }) => Record<string, unknown>;
  onSubmit: (handler?: (values: T) => void) => (e: React.FormEvent) => void;
  /**
   * Checks every rule now and reveals any failures, reporting whether the form
   * is valid. For a wizard that gates a step on validity without submitting.
   */
  validate: () => { hasErrors: boolean; errors: FieldErrors };
  isSubmitting: boolean;
  /**
   * The underlying TanStack form, for sections that have moved off the adapter
   * and want `<form.Field>` directly.
   *
   * Untyped because useForm carries nine inference parameters that cannot be
   * named through a wrapper. Type safety here comes from the surface above,
   * which is what call sites use.
   */
  api: any;
}

/** The store snapshot, narrowed to the parts the adapter reads. */
interface FormState<T> {
  values: T;
  isSubmitting: boolean;
  submissionAttempts: number;
  fieldMeta: Record<string, { isTouched?: boolean } | undefined>;
}

export const useAdaptedForm = <T extends object>({
  initialValues,
  validate: rawValidate,
  validateAsync,
  asyncDebounceMs = DEFAULT_ASYNC_DEBOUNCE,
  onSubmit,
}: AdaptedFormOptions<T>): AdaptedForm<T> => {
  // Normalised once so everything below deals in one shape.
  const validate = useMemo(() => toValidate(rawValidate), [rawValidate]);
  const form: any = useForm({
    defaultValues: initialValues,
    validators: {
      // Registered with TanStack rather than checked in the submit handler, so
      // the library itself refuses to run onSubmit on an invalid form. Checking
      // separately and then calling handleSubmit to mark the attempt ran the
      // submission regardless — an invalid form saved anyway.
      onSubmit: ({ value }: { value: unknown }) => {
        const found = validate ? validate(value as T) : {};
        const fields = Object.fromEntries(
          Object.entries(found).filter(([, message]) => Boolean(message)),
        );
        return Object.keys(fields).length > 0 ? { fields } : undefined;
      },
    },
    onSubmit: async ({ value }: { value: unknown }) => {
      await onSubmit?.(value as T);
    },
  });

  // Subscribing to the whole value set re-renders on every keystroke, which is
  // what Mantine did and what the conditional sections rely on: which fields
  // render depends on `values.type` and on the two form-only switches.
  const values = useStore(form.store, (s: FormState<T>) => s.values);
  const isSubmitting = useStore(form.store, (s: FormState<T>) => s.isSubmitting);

  // Errors are derived rather than stored so they cannot drift from the values
  // they describe.
  const syncErrors = useMemo(() => (validate ? validate(values) : {}), [validate, values]);

  // Async findings are held separately from the derived ones, because they
  // describe an older set of values until the next answer arrives. Merging
  // them into the derived object would make them vanish and reappear on every
  // keystroke.
  const [asyncErrors, setAsyncErrors] = useState<FieldErrors>({});
  const [isValidating, setIsValidating] = useState(false);

  const errors = useMemo(
    // Synchronous rules win: they are about the value on screen right now,
    // whereas an async finding may already be stale.
    () => ({ ...asyncErrors, ...syncErrors }),
    [asyncErrors, syncErrors],
  );

  useEffect(() => {
    if (!validateAsync) return;

    const controller = new AbortController();
    const timer = setTimeout(() => {
      setIsValidating(true);
      validateAsync(values, controller.signal)
        .then((found) => {
          if (!controller.signal.aborted) setAsyncErrors(found);
        })
        .catch(() => {
          // A failed check is not a failed field. Reporting an error because
          // the network was unavailable would block a form that is fine.
          if (!controller.signal.aborted) setAsyncErrors({});
        })
        .finally(() => {
          if (!controller.signal.aborted) setIsValidating(false);
        });
    }, asyncDebounceMs);

    return () => {
      // Cancelling on every change is what keeps a slow answer from landing
      // on top of values it was never about.
      controller.abort();
      clearTimeout(timer);
    };
  }, [validateAsync, values, asyncDebounceMs]);

  // Mantine only shows an error once a field has been touched or the form has
  // been submitted; showing "must be at least 2 characters" on an empty form
  // the user has not typed into yet is noise.
  const submissionAttempts = useStore(form.store, (s: FormState<T>) => s.submissionAttempts);
  const touched = useStore(form.store, (s: FormState<T>) => s.fieldMeta);

  const setFieldValue = useCallback(
    (path: string, value: unknown) => {
      form.setFieldValue(path, value);
    },
    [form],
  );

  const setValues = useCallback(
    (next: Partial<T>) => {
      for (const [path, value] of Object.entries(next)) {
        form.setFieldValue(path, value);
      }
    },
    [form],
  );

  const reset = useCallback(() => {
    form.reset();
    // The dismissal and any async finding belong to the values being thrown
    // away; keeping them would attach an old complaint to a fresh form.
    setAsyncErrors({});
  }, [form]);

  const insertListItem = useCallback(
    (path: string, item: unknown, index?: number) => {
      const current = (readPath(form.store.state.values, path) as unknown[]) ?? [];
      const next = [...current];
      next.splice(index ?? next.length, 0, item);
      form.setFieldValue(path, next);
    },
    [form],
  );

  const removeListItem = useCallback(
    (path: string, index: number) => {
      const current = (readPath(form.store.state.values, path) as unknown[]) ?? [];
      form.setFieldValue(path, current.filter((_, i) => i !== index));
    },
    [form],
  );

  const getInputProps = useCallback(
    (path: string, options?: { type?: 'checkbox' | 'input' }) => {
      const value = readPath(values, path);
      const isTouched = Boolean(touched?.[path]?.isTouched);
      const error = isTouched || submissionAttempts > 0 ? errors[path] : undefined;

      if (options?.type === 'checkbox') {
        return {
          checked: Boolean(value),
          onChange: (e: React.ChangeEvent<HTMLInputElement>) =>
            setFieldValue(path, e.currentTarget.checked),
        };
      }

      return {
        value: value ?? '',
        error,
        onChange: (e: React.ChangeEvent<HTMLInputElement> | string | number | null) => {
          // Mantine's NumberInput and Select hand back a bare value; TextInput
          // hands back the event.
          const next =
            e !== null && typeof e === 'object' && 'currentTarget' in e ? e.currentTarget.value : e;
          setFieldValue(path, next);
        },
        onBlur: () => {
          form.setFieldMeta(path, (m: Record<string, unknown>) => ({ ...m, isTouched: true }));
        },
      };
    },
    [values, errors, touched, submissionAttempts, setFieldValue, form],
  );

  const handleSubmit = useCallback(
    (handler?: (values: T) => void) => (e: React.FormEvent) => {
      e.preventDefault();

      if (!handler) {
        // The validator registered above gates this, so an invalid form stops
        // here and only the attempt count moves.
        void form.handleSubmit();
        return;
      }

      // A caller-supplied handler bypasses the form's own onSubmit, so it has
      // to be gated separately or it would save an invalid form.
      const found = validate ? validate(form.store.state.values as T) : {};
      if (Object.values(found).some(Boolean)) {
        void form.handleSubmit();
        return;
      }
      handler(form.store.state.values as T);
    },
    [form, validate],
  );

  const validateNow = useCallback(() => {
    const found = validate ? validate(form.store.state.values as T) : {};
    const hasErrors = Object.values(found).some(Boolean);
    if (hasErrors) {
      // Nothing is submitted, but the attempt is registered so the errors
      // become visible — otherwise a blocked step gives no reason why.
      void form.handleSubmit();
    }
    return { hasErrors, errors: found };
  }, [form, validate]);

  return {
    values,
    errors,
    isValidating,
    validate: validateNow,
    setFieldValue,
    setValues,
    reset,
    insertListItem,
    removeListItem,
    getInputProps,
    onSubmit: handleSubmit,
    isSubmitting,
    api: form,
  };
};
