import { triggerEventOptions } from '../events';

// The long label from the event picker, so the test and sample views name an
// event the same way the form that subscribes to it does.
export const eventLabel = (event: number): string =>
  triggerEventOptions.find((o) => o.value === event.toString())?.label ?? 'Unknown event';
