import { emailFilterClient as client } from '../../../services/client';
import type { FilterRule } from '../../../api/panmail/v1/email_filter_pb';
import {
  FilterDirection,
  FilterStatus,
} from '../../../api/panmail/v1/email_filter_pb';
import type { MessageInitShape } from '@bufbuild/protobuf';
import type { FilterRuleSchema } from '../../../api/panmail/v1/email_filter_pb';

export const filterService = {
  listRules: () => client.listFilterRules({}),
  createRule: (rule: MessageInitShape<typeof FilterRuleSchema>) => client.createFilterRule({ rule }),
  updateRule: (rule: FilterRule) => client.updateFilterRule({ rule }),
  deleteRule: (id: string) => client.deleteFilterRule({ id }),

  listMessages: (
    direction: FilterDirection = FilterDirection.UNSPECIFIED,
    status: FilterStatus = FilterStatus.PENDING,
    pageSize = 50,
    pageToken = '',
  ) => client.listFilteredMessages({ direction, status, pageSize, pageToken }),

  getMessage: (id: string) => client.getFilteredMessage({ id }),

  // Release puts mail on the wire. The note is recorded against whoever
  // pressed it, so the audit trail says why and not only who.
  release: (id: string, note = '') => client.releaseFilteredMessage({ id, note }),
  reject: (id: string, note = '') => client.rejectFilteredMessage({ id, note }),
};
