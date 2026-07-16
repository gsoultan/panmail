import { Timestamp } from '@bufbuild/protobuf';
import { eventClient } from '../../../services/client';

export const analyticsService = {
  listEvents: async (pageSize = 50, pageToken = '', recipient = '', eventType = 0, startTime?: Date, endTime?: Date, messageId?: string, latestOnly = false) => {
    const res = await eventClient.listEvents({
      pageSize,
      pageToken,
      recipient,
      eventType,
      startTime: startTime ? Timestamp.fromDate(startTime) : undefined,
      endTime: endTime ? Timestamp.fromDate(endTime) : undefined,
      messageId,
      latestOnly,
    });
    return res;
  },
  getMetrics: async (startTime?: Date, endTime?: Date) => {
    const res = await eventClient.getMetrics({
      startTime: startTime ? Timestamp.fromDate(startTime) : undefined,
      endTime: endTime ? Timestamp.fromDate(endTime) : undefined,
    });
    return res;
  },
  getTimeSeriesMetrics: async (startTime?: Date, endTime?: Date, granularity = 'day') => {
    const res = await eventClient.getTimeSeriesMetrics({
      startTime: startTime ? Timestamp.fromDate(startTime) : undefined,
      endTime: endTime ? Timestamp.fromDate(endTime) : undefined,
      granularity,
    });
    return res;
  },
  getEvent: async (id: string) => {
    const res = await eventClient.getEvent({ id });
    return res;
  },
  getPerformanceMetrics: async () => {
    const res = await eventClient.getPerformanceMetrics({});
    return res;
  },
  listArchives: async (pageSize = 50, pageToken = '') => {
    const res = await eventClient.listArchives({ pageSize, pageToken });
    return res;
  },
  downloadArchive: async (id: string) => {
    const res = await eventClient.downloadArchive({ id });
    return res;
  },
};
