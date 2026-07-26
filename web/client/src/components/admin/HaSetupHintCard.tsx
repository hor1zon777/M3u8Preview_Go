import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useNavigate } from 'react-router-dom';
import { Network, X } from 'lucide-react';
import { haApi } from '../../services/haApi.js';
import { adminApi } from '../../services/adminApi.js';

/**
 * Dashboard 首次部署引导卡片：检测到未配置完整 HA 且未被忽略时展示，
 * 询问本机是主节点还是备用节点，导向 /admin/ha 配置向导（预选角色）。
 * "不再提示"落 system_settings（部署级状态，换浏览器/管理员一致生效）。
 */
export function HaSetupHintCard() {
  const navigate = useNavigate();
  const queryClient = useQueryClient();

  const { data: status } = useQuery({
    queryKey: ['ha-status'],
    queryFn: () => haApi.getStatus(),
    staleTime: 60_000,
    retry: false,
  });

  const dismissMutation = useMutation({
    mutationFn: () => adminApi.updateSetting('haSetupDismissed', 'true'),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['ha-status'] }),
  });

  if (!status || status.mode === 'full' || status.setupDismissed) return null;

  return (
    <div className="relative bg-emby-bg-card border border-emby-green/30 rounded-md p-5">
      <button
        onClick={() => dismissMutation.mutate()}
        disabled={dismissMutation.isPending}
        title="不再提示"
        className="absolute top-3 right-3 p-1 rounded text-emby-text-muted hover:text-white transition-colors disabled:opacity-50"
      >
        <X className="w-4 h-4" />
      </button>
      <div className="flex items-start gap-3">
        <div className="w-10 h-10 rounded-lg bg-emby-green/10 border border-emby-green/30 flex items-center justify-center flex-shrink-0">
          <Network className="w-5 h-5 text-emby-green" />
        </div>
        <div className="flex-1 min-w-0">
          <h3 className="text-white font-semibold">配置主备高可用（可选）</h3>
          <p className="text-sm text-emby-text-secondary mt-1">
            检测到当前为单节点部署。用两台服务器可以搭建主备双节点：数据实时复制、
            主节点故障时自动切换。本机将作为主节点还是备用节点？
          </p>
          <div className="flex flex-wrap items-center gap-2 mt-3">
            <button
              onClick={() => navigate('/admin/ha?role=primary')}
              className="px-3 py-1.5 text-sm font-medium rounded-md bg-emby-green text-white hover:bg-emby-green-dark transition-colors"
            >
              配置为主节点
            </button>
            <button
              onClick={() => navigate('/admin/ha?role=replica')}
              className="px-3 py-1.5 text-sm rounded-md bg-emby-bg-elevated border border-emby-border text-emby-text-primary hover:bg-emby-bg-input transition-colors"
            >
              配置为备用节点
            </button>
            <button
              onClick={() => dismissMutation.mutate()}
              disabled={dismissMutation.isPending}
              className="px-3 py-1.5 text-sm text-emby-text-muted hover:text-white transition-colors disabled:opacity-50"
            >
              不再提示
            </button>
          </div>
        </div>
      </div>
    </div>
  );
}
