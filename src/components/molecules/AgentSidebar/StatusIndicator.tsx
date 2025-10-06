import { Box } from '@obolnetwork/obol-ui';
import { Icon } from '../../atoms/Icon';
import { IconName } from '../../atoms/Icon/Icon';

interface StatusIndicatorProps {
  status:
    | 'healthy'
    | 'behind'
    | 'high-usage'
    | 'stalled'
    | 'low-peers'
    | 'disk-bottleneck'
    | 'low-disk';
}

const statusConfig = {
  healthy: {
    text: 'Fully Synced & Healthy',
    icon: 'healthy' as IconName,
    description:
      'Your node is fully caught up, well-connected, and operating within normal resource levels. I can continue to maintain software updates and monitor for any performance drift.',
  },
  behind: {
    text: 'Slightly Behind',
    icon: 'behind' as IconName,
    description:
      'The node is a few dozen blocks behind but recovering steadily. I can adjust peer settings and optimise network configuration to restore full sync speed.',
  },
  'high-usage': {
    text: 'High Resource Usage',
    icon: 'high-usage' as IconName,
    description:
      'The node is keeping pace but running close to CPU or memory limits. I can fine-tune client settings and background processes to reduce the load.',
  },
  stalled: {
    text: 'Stalled Sync',
    icon: 'stalled-sync' as IconName,
    description:
      'Block height has not advanced for some time, suggesting the sync process is stalled. I can review logs, clear temporary data, and restart processes to restore progress.',
  },
  'low-peers': {
    text: 'Low Peer Count',
    icon: 'low-peer' as IconName,
    description:
      'Peer connections are below optimal levels, slowing block propagation. I can adjust firewall rules and peer settings to improve connectivity without changing hardware.',
  },
  'disk-bottleneck': {
    text: 'Disk I/O Bottleneck',
    icon: 'bottleneck' as IconName,
    description:
      'Disk access is slowing database operations, affecting sync performance. I can optimise database settings and enable pruning to reduce I/O demands.',
  },
  'low-disk': {
    text: 'Low Disk Space',
    icon: 'bottleneck' as IconName, // Using bottleneck icon for now, can be changed later
    description:
      'Disk space is critically low, affecting node performance. I can help clean up temporary files and optimize storage usage.',
  },
};

export function StatusIndicator({ status }: StatusIndicatorProps) {
  const config = statusConfig[status];

  return (
    <Box
      style={{
        color: '#FFF',
        textAlign: 'center',
        display: 'flex',
        alignItems: 'center',
        gap: '6px',
        fontFamily: 'DM Sans',
        fontSize: '16px',
        fontStyle: 'normal',
        fontWeight: '600',
        lineHeight: '24px',
      }}
    >
      <Icon name={config.icon} size="s" color="#2fe4ab" />
      {config.text}
    </Box>
  );
}
