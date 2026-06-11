import { useEffect, useState, useCallback } from "react";
import {
  Table,
  Button,
  Modal,
  Form,
  Input,
  Switch,
  Tag,
  Space,
  App,
  Typography,
  Popconfirm,
  Empty,
  Tooltip,
  Card,
  Progress,
  Descriptions,
  List,
  Select,
} from "antd";
import {
  PlusOutlined,
  ReloadOutlined,
  EditOutlined,
  DeleteOutlined,
  CheckCircleOutlined,
  CloseCircleOutlined,
  ThunderboltOutlined,
  StopOutlined,
  PlayCircleOutlined,
  BarChartOutlined,
  DatabaseOutlined,
} from "@ant-design/icons";
import type { ColumnsType } from "antd/es/table";
import {
  listProviders,
  createProvider,
  updateProvider,
  deleteProvider,
  getKiroUsageLimits,
  getKiroModels,
  getAggregatedQuota,
  type ProviderRecord,
  type KiroUsageLimits,
  type KiroModelsResponse,
  type AggregatedQuota,
} from "@/services/api";
import KiroAuthModal from "@/components/KiroAuthModal";
import { useT } from "@/locales";

const { Title, Text } = Typography;

const KIRO_REGION_OPTIONS = [
  "us-east-1",
  "us-west-2",
  "eu-west-1",
  "eu-central-1",
  "ap-southeast-1",
  "ap-northeast-1",
].map((region) => ({ label: region, value: region }));

function StatusBadge({ status, label }: { status: boolean; label: string }) {
  return (
    <span className={`status-badge ${status ? "success" : "error"}`}>
      {label}
    </span>
  );
}

export default function ProvidersPage() {
  const [data, setData] = useState<ProviderRecord[]>([]);
  const [loading, setLoading] = useState(false);
  const [modalOpen, setModalOpen] = useState(false);
  const [editing, setEditing] = useState<ProviderRecord | null>(null);
  const [kiroModalOpen, setKiroModalOpen] = useState(false);
  const [selectedProvider, setSelectedProvider] = useState<ProviderRecord | null>(null);
  const [quotaOpen, setQuotaOpen] = useState(false);
  const [quotaLoading, setQuotaLoading] = useState(false);
  const [quota, setQuota] = useState<KiroUsageLimits | null>(null);
  const [modelsOpen, setModelsOpen] = useState(false);
  const [modelsLoading, setModelsLoading] = useState(false);
  const [models, setModels] = useState<KiroModelsResponse | null>(null);
  const [aggregated, setAggregated] = useState<AggregatedQuota | null>(null);
  const [form] = Form.useForm();
  const { message } = App.useApp();
  const t = useT();

  const fetchData = useCallback(async () => {
    setLoading(true);
    try {
      const res = await listProviders();
      setData(res.accounts);
      const agg = await getAggregatedQuota(res.accounts);
      setAggregated(agg);
    } catch {
      message.error(t.providers.loadError);
    } finally {
      setLoading(false);
    }
  }, [message, t]);

  useEffect(() => {
    fetchData();
  }, [fetchData]);

  const openCreate = () => {
    setEditing(null);
    form.resetFields();
    form.setFieldsValue({ enabled: true, region: "us-east-1" });
    setModalOpen(true);
  };

  const openEdit = (record: ProviderRecord) => {
    setEditing(record);
    form.setFieldsValue({
      name: record.name,
      region: record.region || "us-east-1",
      enabled: record.enabled,
    });
    setModalOpen(true);
  };

  const openKiroAuth = (record: ProviderRecord) => {
    setSelectedProvider(record);
    setKiroModalOpen(true);
  };

  const loadQuota = async (record: ProviderRecord, refresh = false) => {
    setQuotaLoading(true);
    try {
      const nextQuota = await getKiroUsageLimits(record.name, refresh);
      setQuota(nextQuota);
      if (refresh) {
        message.success(t.providers.quotaRefreshSuccess);
        fetchData();
      }
    } catch (err) {
      message.error(err instanceof Error ? err.message : t.providers.quotaLoadError);
    } finally {
      setQuotaLoading(false);
    }
  };

  const openQuota = async (record: ProviderRecord) => {
    setSelectedProvider(record);
    setQuota(record.usage_limits ?? null);
    setQuotaOpen(true);
    if (!record.usage_limits) {
      await loadQuota(record);
    }
  };

  const openModels = async (record: ProviderRecord) => {
    setSelectedProvider(record);
    setModels(null);
    setModelsOpen(true);
    setModelsLoading(true);
    try {
      setModels(await getKiroModels(record.name));
    } catch (err) {
      message.error(err instanceof Error ? err.message : t.providers.modelsLoadError);
    } finally {
      setModelsLoading(false);
    }
  };

  const handleSubmit = async () => {
    try {
      const values = await form.validateFields();
      const payload = {
        name: values.name,
        region: values.region,
        enabled: values.enabled,
        type: "kiro",
      };

      if (editing) {
        await updateProvider(editing.id, payload);
        message.success(t.providers.updateSuccess);
      } else {
        await createProvider(payload);
        message.success(t.providers.createSuccess);
      }
      setModalOpen(false);
      fetchData();
    } catch {
      // validation error, ignore
    }
  };

  const handleDelete = async (id: string) => {
    try {
      await deleteProvider(id);
      message.success(t.providers.deleteSuccess);
      fetchData();
    } catch {
      message.error(t.providers.deleteError);
    }
  };

  const handleToggleEnabled = async (record: ProviderRecord) => {
    try {
      await updateProvider(record.id, { enabled: !record.enabled });
      message.success(record.enabled ? t.providers.disableSuccess : t.providers.enableSuccess);
      fetchData();
    } catch {
      message.error(t.providers.updateError);
    }
  };

  const columns: ColumnsType<ProviderRecord> = [
    {
      title: t.common.id,
      dataIndex: "id",
      width: 100,
      render: (id: string) => (
        <Tooltip title={id}>
          <Tag
            className="font-mono text-xs cursor-pointer hover:opacity-80 transition-opacity"
            onClick={() => {
              navigator.clipboard.writeText(id);
              message.success(t.common.copied);
            }}
          >
            {id ? id.slice(0, 8) : "-"}
          </Tag>
        </Tooltip>
      ),
    },
    {
      title: t.common.name,
      dataIndex: "name",
      width: 180,
      render: (name: string) => <Text strong>{name}</Text>,
    },
    {
      title: t.providers.fieldRegion,
      dataIndex: "region",
      width: 130,
      render: (region: string) => <Tag className="font-mono text-xs">{region || "us-east-1"}</Tag>,
    },
    {
      title: t.common.enabled,
      dataIndex: "enabled",
      width: 90,
      align: "center",
      render: (v: boolean) => <StatusBadge status={v} label={v ? t.common.yes : t.common.no} />,
    },
    {
      title: t.common.status,
      dataIndex: "healthy",
      width: 90,
      align: "center",
      render: (v: boolean) =>
        v ? (
          <Tooltip title={t.providers.healthy}>
            <CheckCircleOutlined className="text-green-500 text-lg" />
          </Tooltip>
        ) : (
          <Tooltip title={t.providers.unhealthy}>
            <CloseCircleOutlined className="text-red-400 text-lg" />
          </Tooltip>
        ),
    },
    {
      title: t.providers.recentQuota,
      dataIndex: "usage_limits",
      width: 220,
      render: (quota?: KiroUsageLimits) =>
        quota ? (
          <div className="min-w-[180px]">
            <div className="flex items-center justify-between gap-2 mb-1">
              <Text className="text-xs">
                {formatNumber(quota.usage.used_precise || quota.usage.used)} / {formatNumber(quota.usage.limit_precise || quota.usage.limit)}
              </Text>
              <Text type="secondary" className="text-xs">
                {formatPercent(quota.usage.percent_used)}%
              </Text>
            </div>
            <Progress
              percent={Math.min(100, quota.usage.percent_used || 0)}
              size="small"
              showInfo={false}
              strokeColor={quotaColor(quota.usage.percent_used || 0)}
            />
            <Text type="secondary" className="text-xs">
              {quota.fetched_at ? new Date(quota.fetched_at).toLocaleString() : "-"}
            </Text>
          </div>
        ) : (
          <Text type="secondary" className="text-xs">{t.providers.quotaNoData}</Text>
        ),
    },
    {
      title: t.common.actions,
      fixed: "right",
      width: 250,
      render: (_, record) => (
        <Space size="small">
          <Tooltip title={t.kiro.pkceLogin}>
            <Button
              type="link"
              size="small"
              icon={<ThunderboltOutlined />}
              onClick={() => openKiroAuth(record)}
              className="text-orange-500"
            >
              {t.providers.authorize}
            </Button>
          </Tooltip>
          <Tooltip title={t.providers.quota}>
            <Button
              type="text"
              size="small"
              icon={<BarChartOutlined />}
              onClick={() => openQuota(record)}
              className="text-blue-500"
            />
          </Tooltip>
          <Tooltip title={t.providers.models}>
            <Button
              type="text"
              size="small"
              icon={<DatabaseOutlined />}
              onClick={() => openModels(record)}
              className="text-purple-500"
            />
          </Tooltip>
          <Tooltip title={record.enabled ? t.providers.disable : t.providers.enable}>
            <Button
              type="text"
              size="small"
              icon={record.enabled ? <StopOutlined /> : <PlayCircleOutlined />}
              onClick={() => handleToggleEnabled(record)}
              className={record.enabled ? "text-orange-500" : "text-green-500"}
            />
          </Tooltip>
          <Button type="text" size="small" icon={<EditOutlined />} onClick={() => openEdit(record)} />
          <Popconfirm
            title={t.providers.deleteConfirm}
            description={t.providers.deleteDesc}
            onConfirm={() => handleDelete(record.id)}
            okButtonProps={{ danger: true }}
          >
            <Button type="text" size="small" danger icon={<DeleteOutlined />} />
          </Popconfirm>
        </Space>
      ),
    },
  ];

  return (
    <div>
      <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-4 mb-6">
        <Title level={4} className="!mb-0">
          {t.providers.title}
        </Title>
        <Space>
          <Button icon={<ReloadOutlined />} onClick={fetchData} loading={loading}>
            {t.common.refresh}
          </Button>
          <Button type="primary" icon={<PlusOutlined />} onClick={openCreate}>
            {t.providers.addProvider}
          </Button>
        </Space>
      </div>

      {aggregated?.hasData && (
        <Card className="mb-5" size="small">
          <div className="flex flex-col sm:flex-row sm:items-center gap-3">
            <div className="flex items-center gap-3 flex-1 min-w-0">
              <div>
                <Text type="secondary" className="text-xs">{t.providers.quotaTotal}</Text>
                <div className="flex items-baseline gap-2 mt-0.5">
                  <span className="font-semibold text-base" style={{ color: quotaColor(aggregated.percentUsed) }}>
                    {formatNumber(aggregated.totalUsed)} / {formatNumber(aggregated.totalLimit)}
                  </span>
                  <Tag color={aggregated.percentUsed >= 90 ? "red" : aggregated.percentUsed >= 70 ? "orange" : "green"}>
                    {formatPercent(aggregated.percentUsed)}%
                  </Tag>
                  <Text type="secondary" className="text-xs hidden sm:inline">
                    ({t.providers.quotaAggAccounts.replace("{n}", String(aggregated.accountCount))})
                  </Text>
                </div>
              </div>
            </div>
            <div className="flex-1 min-w-[160px]">
              <Progress
                percent={Math.min(100, aggregated.percentUsed)}
                showInfo={false}
                strokeColor={quotaColor(aggregated.percentUsed)}
                size="small"
              />
            </div>
          </div>
        </Card>
      )}

      <Card className="overflow-hidden">
        <Table
          rowKey="id"
          columns={columns}
          dataSource={data}
          loading={loading}
          pagination={false}
          size="middle"
          scroll={{ x: 1040 }}
          locale={{
            emptyText: (
              <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description={t.empty.noProviders}>
                <Button type="primary" icon={<PlusOutlined />} onClick={openCreate}>
                  {t.empty.createFirstProvider}
                </Button>
              </Empty>
            ),
          }}
        />
      </Card>

      <Modal
        title={editing ? t.providers.editProvider : t.providers.addProvider}
        open={modalOpen}
        onOk={handleSubmit}
        onCancel={() => setModalOpen(false)}
        okText={editing ? t.common.save : t.common.create}
        cancelText={t.common.cancel}
        width={520}
        destroyOnClose
      >
        <Form form={form} layout="vertical" className="mt-4">
          <Form.Item
            name="name"
            label={t.providers.fieldName}
            rules={[
              { required: true, message: t.common.required },
              { pattern: /^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$/, message: t.providers.fieldNameRule },
            ]}
          >
            <Input placeholder={t.providers.fieldNamePlaceholder} disabled={!!editing} />
          </Form.Item>
          <Form.Item
            name="region"
            label={t.providers.fieldRegion}
            rules={[{ required: true, message: t.common.required }]}
          >
            <Select
              showSearch
              placeholder={t.providers.fieldRegionPlaceholder}
              options={KIRO_REGION_OPTIONS}
            />
          </Form.Item>
          {editing && (
            <Form.Item name="enabled" label={t.providers.fieldEnabled} valuePropName="checked">
              <Switch />
            </Form.Item>
          )}
        </Form>
      </Modal>

      {selectedProvider && (
        <KiroAuthModal
          open={kiroModalOpen}
          providerName={selectedProvider.name}
          providerRegion={selectedProvider.region}
          onClose={() => {
            setKiroModalOpen(false);
            setSelectedProvider(null);
            fetchData();
          }}
        />
      )}

      <Modal
        title={`${t.providers.quota} - ${selectedProvider?.name ?? ""}`}
        open={quotaOpen}
        onCancel={() => {
          setQuotaOpen(false);
          setQuota(null);
        }}
        footer={
          <Space>
            <Button
              icon={<ReloadOutlined />}
              loading={quotaLoading}
              onClick={() => selectedProvider && loadQuota(selectedProvider, true)}
            >
              {t.providers.quotaRefreshLatest}
            </Button>
            <Button onClick={() => setQuotaOpen(false)}>{t.common.close}</Button>
          </Space>
        }
        width={620}
        destroyOnClose
      >
        {quotaLoading ? (
          <div className="flex justify-center py-10">
            <div className="w-8 h-8 rounded-full border-2 border-blue-500 border-t-transparent animate-spin" />
          </div>
        ) : quota ? (
          <div className="space-y-5">
            <div>
              <div className="flex items-center justify-between mb-2">
                <Text strong>{quota.usage.display_name || quota.usage.resource_type || t.providers.quotaCredits}</Text>
                <Text type="secondary">
                  {formatNumber(quota.usage.used_precise || quota.usage.used)} / {formatNumber(quota.usage.limit_precise || quota.usage.limit)}
                </Text>
              </div>
              <Progress
                percent={Math.min(100, quota.usage.percent_used || 0)}
                format={(percent) => `${formatPercent(percent)}%`}
                strokeColor={quotaColor(quota.usage.percent_used || 0)}
              />
            </div>
            <Descriptions column={2} bordered size="small">
              <Descriptions.Item label={t.providers.quotaTier}>{quota.tier || "-"}</Descriptions.Item>
              <Descriptions.Item label={t.providers.quotaRemaining}>
                {formatNumber(quota.usage.remaining_precise || quota.usage.remaining)}
              </Descriptions.Item>
              <Descriptions.Item label={t.providers.quotaResetDays}>{quota.days_until_reset ?? "-"}</Descriptions.Item>
              <Descriptions.Item label={t.providers.quotaEmail}>{quota.email || "-"}</Descriptions.Item>
              <Descriptions.Item label={t.providers.quotaStatus}>{quota.subscription_state || "-"}</Descriptions.Item>
              <Descriptions.Item label={t.providers.quotaFetchedAt}>{quota.fetched_at ? new Date(quota.fetched_at).toLocaleString() : "-"}</Descriptions.Item>
            </Descriptions>
          </div>
        ) : (
          <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description={t.providers.quotaNoData} />
        )}
      </Modal>

      <Modal
        title={`${t.providers.models} - ${selectedProvider?.name ?? ""}`}
        open={modelsOpen}
        onCancel={() => {
          setModelsOpen(false);
          setModels(null);
        }}
        footer={null}
        width={920}
        className="models-modal"
        destroyOnClose
      >
        {modelsLoading ? (
          <div className="flex justify-center py-10">
            <div className="w-8 h-8 rounded-full border-2 border-blue-500 border-t-transparent animate-spin" />
          </div>
        ) : models?.models?.length ? (
          <div className="models-panel">
            <div className="models-summary">
              <span className="models-summary-item">
                <DatabaseOutlined />
                {t.providers.modelsTotal}: <strong>{models.total}</strong>
              </span>
              {models.models.find((model) => model.is_default) && (
                <span className="models-summary-item models-summary-default">
                  {t.providers.modelsKiroDefault}: <strong>{models.models.find((model) => model.is_default)?.model_id}</strong>
                </span>
              )}
            </div>
            <List
              dataSource={models.models}
              className="models-list"
              renderItem={(model) => (
                <List.Item
                  key={model.model_id}
                  className={`models-row ${model.is_default ? "is-default" : ""}`}
                >
                  <div className="models-row-main">
                    <div className="models-row-title">
                      <div className="min-w-0">
                        <Text
                          strong
                          copyable={{ text: model.model_id }}
                          className="models-id"
                        >
                          {model.model_id}
                        </Text>
                        <Text type="secondary" className="models-description">
                          {model.description || "-"}
                        </Text>
                      </div>
                      <Tag className="models-rate">
                        {formatRate(model.rate_multiplier, model.rate_unit)}
                      </Tag>
                    </div>

                    <div className="models-meta">
                      <div className="models-meta-cell">
                        <Text type="secondary" className="models-meta-label">
                          {t.providers.modelsInputTypes}
                        </Text>
                        <div className="models-tag-list">
                          {(model.supported_input_types?.length ? model.supported_input_types : ["-"]).map((type) => (
                            <Tag key={type} className="models-type-tag">{type}</Tag>
                          ))}
                        </div>
                      </div>
                      <div className="models-meta-cell">
                        <Text type="secondary" className="models-meta-label">
                          {t.providers.modelsTokenLimits}
                        </Text>
                        <Text className="models-token-limit">
                          {formatTokenLimits(model.token_limits)}
                        </Text>
                      </div>
                      <div className="models-meta-cell">
                        <Text type="secondary" className="models-meta-label">
                          {t.providers.modelsPromptCaching}
                        </Text>
                        <Tag className={model.prompt_caching?.supports_prompt_caching ? "models-cache-tag is-on" : "models-cache-tag"}>
                          {model.prompt_caching?.supports_prompt_caching ? t.common.yes : t.common.no}
                        </Tag>
                      </div>
                    </div>
                  </div>
                </List.Item>
              )}
            />
          </div>
        ) : (
          <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description={t.providers.modelsNoData} />
        )}
      </Modal>
    </div>
  );
}

function formatNumber(value?: number) {
  if (value == null || Number.isNaN(value)) return "-";
  return new Intl.NumberFormat(undefined, { maximumFractionDigits: 2 }).format(value);
}

function formatPercent(value?: number) {
  if (value == null || Number.isNaN(value)) return "0.00";
  return new Intl.NumberFormat(undefined, {
    minimumFractionDigits: 2,
    maximumFractionDigits: 2,
  }).format(value);
}

function quotaColor(pct: number) {
  if (pct >= 90) return "#ef4444";
  if (pct >= 70) return "#f97316";
  return "#10b981";
}

function formatTokenLimits(limits?: { max_input_tokens: number; max_output_tokens: number }) {
  if (!limits) return "-";
  return `${formatNumber(limits.max_input_tokens)} in / ${formatNumber(limits.max_output_tokens)} out`;
}

function formatRate(multiplier?: number, unit?: string) {
  if (multiplier == null || Number.isNaN(multiplier)) return "-";
  return `${multiplier}x ${unit || ""}`.trim();
}
