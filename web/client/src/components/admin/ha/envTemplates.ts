/**
 * HA 配置向导的模板生成（纯函数，便于单测）。
 *
 * 所有输入（含 Cloudflare Token）只在浏览器内存中拼接文本，绝不发送到服务器。
 */

export interface HaWizardForm {
  /** 本机是主（preferred）还是备。只影响结果页里"本机"标注在哪一栏。 */
  localRole: 'primary' | 'replica';
  /** 用户流量域名（A 记录全名），如 media.example.com */
  domain: string;
  /** 租约 TXT 记录全名 */
  leaseRecord: string;
  /** 交还请求 TXT 记录全名 */
  handoffRecord: string;
  cfApiToken: string;
  cfZoneId: string;
  /** 主（preferred）节点 */
  primaryId: string;
  primaryIp: string;
  primaryHost: string;
  /** 备节点 */
  replicaId: string;
  replicaIp: string;
  replicaHost: string;
  /** LiteFS 节点间通道端口 */
  channelPort: string;
}

/** 按域名派生 TXT 记录名默认值：media.example.com → _ha-lease.example.com。 */
export function deriveRecordNames(domain: string): { lease: string; handoff: string } {
  const d = domain.trim();
  const labels = d.split('.').filter(Boolean);
  // 三段及以上认为第一段是子域，记录挂在父域下；两段（apex）直接挂在本域下。
  const base = labels.length >= 3 ? labels.slice(1).join('.') : d;
  return { lease: base ? `_ha-lease.${base}` : '', handoff: base ? `_ha-handoff.${base}` : '' };
}

/** 生成单台节点的 .env 片段。preferred 决定 NODE/PEER 方向与 HA_PREFERRED。 */
export function buildEnvSnippet(form: HaWizardForm, preferred: boolean): string {
  const self = preferred
    ? { id: form.primaryId, ip: form.primaryIp, host: form.primaryHost }
    : { id: form.replicaId, ip: form.replicaIp, host: form.replicaHost };
  const peer = preferred
    ? { id: form.replicaId, ip: form.replicaIp, host: form.replicaHost }
    : { id: form.primaryId, ip: form.primaryIp, host: form.primaryHost };
  const port = form.channelPort || '20203';

  return [
    `# ===== 主备高可用（${self.id} / ${preferred ? '主节点 preferred' : '备用节点'}）=====`,
    `# 追加到本机 .env 末尾；两台的 JWT_SECRET / JWT_REFRESH_SECRET / PROXY_SECRET 必须相同`,
    `HA_NODE_ID=${self.id}`,
    `HA_PEER_ID=${peer.id}`,
    `HA_PREFERRED=${preferred}`,
    ``,
    `HA_PEER_HOSTNAME=${peer.host}`,
    `HA_SELF_ADVERTISE_URL=https://${self.host}:${port}`,
    `HA_PEER_ADVERTISE_URL=https://${peer.host}:${port}`,
    `HA_PEER_BASE_URL=https://${peer.host}:${port}`,
    `HA_SELF_PUBLIC_IP=${self.ip}`,
    `HA_PEER_PUBLIC_IP=${peer.ip}`,
    `LITEFS_CHANNEL_PORT=${port}`,
    ``,
    `CF_API_TOKEN=${form.cfApiToken}`,
    `CF_ZONE_ID=${form.cfZoneId}`,
    `HA_DNS_RECORD=${form.domain}`,
    `HA_LEASE_RECORD=${form.leaseRecord}`,
    `HA_HANDOFF_RECORD=${form.handoffRecord}`,
  ].join('\n');
}

export interface ChecklistItem {
  title: string;
  detail: string;
  /** 可复制的命令/内容（多行）。 */
  command?: string;
}

/** 生成部署操作清单（与 docs/ha-failover.md / README 的手工步骤一致）。 */
export function buildChecklist(form: HaWizardForm): ChecklistItem[] {
  return [
    {
      title: '两台服务器确认内核支持 FUSE',
      detail: 'KVM 型 VPS 基本都有；OpenVZ / LXC 容器型 VPS 可能没有，那样 LiteFS 方案不可行。',
      command: 'ls -l /dev/fuse',
    },
    {
      title: 'Cloudflare 手工创建 3 条 DNS 记录',
      detail:
        `A 记录 ${form.domain} → ${form.primaryIp}（保持橙云代理）；` +
        `TXT ${form.leaseRecord} 与 TXT ${form.handoffRecord} 各建一条（初始内容如下，代码只更新不创建）。` +
        `API Token 权限收窄到 Zone → DNS → Edit，且只授权这一个 zone。`,
      command: [
        `# TXT ${form.leaseRecord} 初始内容：`,
        `v=1;owner=;epoch=0;exp=0;state=active`,
        `# TXT ${form.handoffRecord} 初始内容：`,
        `v=1;want=;txid=`,
      ].join('\n'),
    },
    {
      title: '生成节点间通道自签证书（两台各执行，放 ./certs）',
      detail:
        '每台生成自己的 node.crt / node.key（CN 为本机内部主机名），再把对方的 node.crt 复制过来存为 peer-ca.crt。',
      command: [
        `# 在 ${form.primaryId} 上：`,
        `openssl req -x509 -newkey rsa:2048 -nodes -days 3650 \\`,
        `  -keyout certs/node.key -out certs/node.crt -subj "/CN=${form.primaryHost}" \\`,
        `  -addext "subjectAltName=DNS:${form.primaryHost}"`,
        `# 在 ${form.replicaId} 上：`,
        `openssl req -x509 -newkey rsa:2048 -nodes -days 3650 \\`,
        `  -keyout certs/node.key -out certs/node.crt -subj "/CN=${form.replicaHost}" \\`,
        `  -addext "subjectAltName=DNS:${form.replicaHost}"`,
        `# 互换：把对方的 node.crt 复制到本机 certs/peer-ca.crt`,
      ].join('\n'),
    },
    {
      title: '同步密钥',
      detail:
        '两台 .env 的 JWT_SECRET / JWT_REFRESH_SECRET / PROXY_SECRET 必须完全相同；data/ecdh.pem 从主节点复制到备节点。',
      command: `scp data/ecdh.pem <备节点>:<项目目录>/data/ecdh.pem`,
    },
    {
      title: '把上面生成的 .env 片段分别追加到两台的 .env，然后启动',
      detail: `先启动主节点（${form.primaryId}），确认健康后再启动备节点（${form.replicaId}）。`,
      command: 'docker compose -f docker-compose.yml -f docker-compose.ha.yml up -d',
    },
    {
      title: '（已有数据时）迁移存量数据库',
      detail:
        '禁止直接 cp 进 FUSE 目录！必须用 litefs import 导入；字幕 VTT 正文由后台幂等回填，无需手工处理。',
      command: 'docker compose exec app litefs import -name m3u8preview.db /data/m3u8preview.db',
    },
    {
      title: '验证',
      detail:
        `在两台服务器上分别执行，role 字段应一主一备；再回到本页面查看状态面板。` +
        `若备节点日志报 appuser 无法写 /litefs，在其 .env 加 LITEFS_RUN_AS_ROOT=1 后重启。`,
      command: [
        `docker compose logs app --tail 20 | grep "\\[ha\\]"   # 查看角色决议日志`,
        `docker compose exec app curl -s http://127.0.0.1:3000/api/health   # 看 role 字段`,
      ].join('\n'),
    },
  ];
}
