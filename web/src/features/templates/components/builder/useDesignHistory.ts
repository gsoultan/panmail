import { useCallback, useReducer } from 'react';
import { EmailDesign } from './types';

/**
 * Undo/redo for the email design.
 *
 * The logic is a pure reducer rather than a tangle of useState calls, so it can
 * be tested without React or a DOM — including the coalescing rule, which is
 * the part most likely to break subtly. `now` is passed in with the action for
 * the same reason: a reducer that reads the clock is not a reducer.
 *
 * Two details make the difference between this being useful and being noise.
 *
 * Consecutive edits inside a short window collapse into one entry. Every
 * keystroke in a text field is a state update, so without coalescing a single
 * typed sentence becomes forty undo steps and Ctrl-Z stops meaning anything.
 *
 * The stack is bounded. A design can hold base64 images and deeply nested
 * blocks, and an unbounded stack of them is a session-length leak in a tab
 * people leave open all day.
 */

export const COALESCE_MS = 600;
export const MAX_HISTORY = 50;

export interface HistoryState {
  past: EmailDesign[];
  present: EmailDesign;
  future: EmailDesign[];
  lastCommitAt: number;
}

export type HistoryAction =
  // The action carries the updater rather than a resolved design, so the
  // reducer applies it against whatever `present` actually is at the time React
  // processes the action. Resolving it at the call site instead would need a
  // ref read during render and would compute against a stale design if two
  // updates land in the same batch.
  | { type: 'set'; updater: (prev: EmailDesign) => EmailDesign; coalesce?: boolean; now: number }
  | { type: 'undo' }
  | { type: 'redo' }
  | { type: 'reset'; design: EmailDesign };

export const initHistory = (present: EmailDesign): HistoryState => ({
  past: [],
  present,
  future: [],
  lastCommitAt: 0,
});

export const historyReducer = (state: HistoryState, action: HistoryAction): HistoryState => {
  switch (action.type) {
    case 'set': {
      const next = action.updater(state.present);
      if (next === state.present) return state;

      const withinWindow = action.now - state.lastCommitAt < COALESCE_MS;
      // Amending the top of the stack rather than pushing is what keeps a burst
      // of typing to a single undo step.
      const amend = Boolean(action.coalesce) && withinWindow;
      const past = amend ? state.past : [...state.past, state.present];

      return {
        past: past.length > MAX_HISTORY ? past.slice(past.length - MAX_HISTORY) : past,
        present: next,
        // A new edit invalidates a redo branch the user walked away from.
        future: [],
        lastCommitAt: action.now,
      };
    }

    case 'undo': {
      if (state.past.length === 0) return state;
      return {
        past: state.past.slice(0, -1),
        present: state.past[state.past.length - 1],
        future: [state.present, ...state.future],
        // Zeroed so the next edit cannot be coalesced into the state we just
        // stepped back to — that would silently eat the undo.
        lastCommitAt: 0,
      };
    }

    case 'redo': {
      if (state.future.length === 0) return state;
      return {
        past: [...state.past, state.present],
        present: state.future[0],
        future: state.future.slice(1),
        lastCommitAt: 0,
      };
    }

    // Loading a different template must not leave the previous one reachable
    // through undo.
    case 'reset':
      return initHistory(action.design);

    default:
      return state;
  }
};

export interface DesignHistory {
  design: EmailDesign;
  setDesign: (
    updater: (prev: EmailDesign) => EmailDesign,
    options?: { coalesce?: boolean },
  ) => void;
  reset: (design: EmailDesign) => void;
  undo: () => void;
  redo: () => void;
  canUndo: boolean;
  canRedo: boolean;
}

export const useDesignHistory = (initial: EmailDesign): DesignHistory => {
  const [state, dispatch] = useReducer(historyReducer, initial, initHistory);

  const setDesign = useCallback<DesignHistory['setDesign']>((updater, options) => {
    dispatch({ type: 'set', updater, coalesce: options?.coalesce, now: Date.now() });
  }, []);

  const reset = useCallback((design: EmailDesign) => dispatch({ type: 'reset', design }), []);
  const undo = useCallback(() => dispatch({ type: 'undo' }), []);
  const redo = useCallback(() => dispatch({ type: 'redo' }), []);

  return {
    design: state.present,
    setDesign,
    reset,
    undo,
    redo,
    canUndo: state.past.length > 0,
    canRedo: state.future.length > 0,
  };
};
