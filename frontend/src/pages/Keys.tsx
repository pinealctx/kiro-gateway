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
  Card,
  Alert,
  Tooltip,
  Select,
} from "antd";
import {
  PlusOutlined,
  ReloadOutlined,
  CopyOutlined,
  EditOutlined,
  DeleteOutlined,
  KeyOutlined,
  BarChartOutlined,
} from "@ant-design/icons";
import type { ColumnsType } from "antd/es/table";
import {
  listKeys,
  createKey,
  updateKey,
  deleteKey,
  listProviders,
  type ApiKey,
  type ProviderRecord,
} from "@/services/api";
import UsageModal from "@/components/UsageModal";
import { useT } from "@/locales";

const { Title, Text } = Typography;

// Status Badge Component
function StatusBadge({ status, label }: { status: boolean; label: string }) {
  return (
    <span className={`status-badge ${status ? "success" : "error"}`}>
      {label}
    </span>
  );
}

export default function KeysPage() {
  const [data, setData] = useState<ApiKey[]>([]);
  const [accounts, setAccounts] = useState<ProviderRecord[]>([]);
  const [loading, setLoading] = useState(false);
  const [modalOpen, setModalOpen] = useState(false);
  const [editing, setEditing] = useState<ApiKey | null>(null);
  const [newKeyValue, setNewKeyValue] = useState("");
  const [form] = Form.useForm();
  const selectedKiroAccounts: string[] = Form.useWatch("kiro_accounts", form) ?? [];
  const { message } = App.useApp();
  const t = useT();

  // Usage modal state
  const [usageModalOpen, setUsageModalOpen] = useState(false);
  const [selectedKey, setSelectedKey] = useState<ApiKey | null>(null);

  const fetchData = useCallback(async () => {
    setLoading(true);
    try {
      const [keysRes, accountsRes] = await Promise.all([listKeys(), listProviders()]);
      setData(keysRes.keys);
      setAccounts(accountsRes.accounts);
    } catch {
      message.error(t.keys.loadError);
    } finally {
      setLoading(false);
    }
  }, [message, t]);

  useEffect(() => {
    fetchData();
  }, [fetchData]);

  useEffect(() => {
    if (!modalOpen || newKeyValue) return;
    const current = form.getFieldValue("kiro_default_account");
    if (!selectedKiroAccounts.length) {
      if (current) form.setFieldValue("kiro_default_account", undefined);
      return;
    }
    if (!current || !selectedKiroAccounts.includes(current)) {
      form.setFieldValue("kiro_default_account", selectedKiroAccounts[0]);
    }
  }, [form, modalOpen, newKeyValue, selectedKiroAccounts]);

  const openCreate = () => {
    setEditing(null);
    setNewKeyValue("");
    form.resetFields();
    form.setFieldsValue({ enabled: true, suppress_reasoning: false });
    setModalOpen(true);
  };

  const openEdit = (record: ApiKey) => {
    setEditing(record);
    setNewKeyValue("");
    form.setFieldsValue({
      ...record,
      kiro_accounts: record.kiro_accounts ?? [],
      kiro_default_account: record.kiro_default_account ?? record.kiro_accounts?.[0],
    });
    setModalOpen(true);
  };

  const openUsage = (record: ApiKey) => {
    setSelectedKey(record);
    setUsageModalOpen(true);
  };

  const handleSubmit = async () => {
    try {
      const values = await form.validateFields();

      const payload = {
        ...values,
        kiro_accounts: values.kiro_accounts,
        kiro_default_account: values.kiro_default_account ?? values.kiro_accounts?.[0],
      };

      if (editing) {
        await updateKey(editing.id, payload);
        message.success(t.keys.updateSuccess);
        setModalOpen(false);
      } else {
        const res = await createKey(payload);
        if (res.key) {
          setNewKeyValue(res.key);
          message.success(t.keys.createSuccess);
        } else {
          setModalOpen(false);
        }
      }
      fetchData();
    } catch {
      // validation error
    }
  };

  const handleDelete = async (id: string) => {
    try {
      await deleteKey(id);
      message.success(t.keys.deleteSuccess);
      fetchData();
    } catch {
      message.error(t.keys.deleteError);
    }
  };

  const handleCopyKey = () => {
    navigator.clipboard.writeText(newKeyValue);
    message.success(t.common.copied);
  };

  const columns: ColumnsType<ApiKey> = [
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
            {id ? id.slice(0, 8) : "—"}
          </Tag>
        </Tooltip>
      ),
    },
    {
      title: t.common.name,
      dataIndex: "name",
      width: 200,
      render: (name: string) => <Text strong>{name}</Text>,
    },
    {
      title: t.keys.keyPrefix,
      dataIndex: "key_prefix",
      width: 150,
      render: (v: string) => (
        <Tooltip title={t.keys.keyPrefixTooltip}>
          <Text code className="text-xs">
            {v}...
          </Text>
        </Tooltip>
      ),
    },
    {
      title: t.common.enabled,
      dataIndex: "enabled",
      width: 80,
      align: "center",
      render: (v: boolean) => <StatusBadge status={v} label={v ? t.common.yes : t.common.no} />,
    },
    {
      title: t.keys.fieldKiroAccounts,
      dataIndex: "kiro_accounts",
      ellipsis: true,
      render: (v: string[], record) =>
        v?.length ? (
          <div className="flex flex-wrap gap-1">
            {v.map((account) => (
              <Tag key={account} color={account === record.kiro_default_account ? "blue" : undefined}>
                {account}
                {account === record.kiro_default_account ? ` ${t.keys.defaultAccountTag}` : ""}
              </Tag>
            ))}
          </div>
        ) : (
          <Text type="secondary">-</Text>
        ),
    },
    {
      title: t.keys.fieldSuppressReasoning,
      dataIndex: "suppress_reasoning",
      width: 110,
      align: "center",
      render: (v: boolean) => <StatusBadge status={!!v} label={v ? t.common.yes : t.common.no} />,
    },
    {
      title: t.common.actions,
      width: 100,
      fixed: "right",
      render: (_, record) => (
        <Space size="small">
          <Tooltip title={t.keys.usage}>
            <Button
              type="text"
              size="small"
              icon={<BarChartOutlined />}
              onClick={() => openUsage(record)}
              className="text-blue-500"
            />
          </Tooltip>
          <Button
            type="text"
            size="small"
            icon={<EditOutlined />}
            onClick={() => openEdit(record)}
          />
          <Popconfirm
            title={t.keys.deleteConfirm}
            description={t.keys.deleteDesc}
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
          {t.keys.title}
        </Title>
        <Space>
          <Button icon={<ReloadOutlined />} onClick={fetchData} loading={loading}>
            {t.common.refresh}
          </Button>
          <Button type="primary" icon={<PlusOutlined />} onClick={openCreate}>
            {t.keys.createKey}
          </Button>
        </Space>
      </div>

      <Card className="overflow-hidden">
        <Table
          rowKey="id"
          columns={columns}
          dataSource={data}
          loading={loading}
          pagination={false}
          size="middle"
          scroll={{ x: 550 }}
          locale={{
            emptyText: (
              <Empty
                image={Empty.PRESENTED_IMAGE_SIMPLE}
                description={t.empty.noKeys}
              >
                <Button type="primary" icon={<PlusOutlined />} onClick={openCreate}>
                  {t.empty.createFirstKey}
                </Button>
              </Empty>
            ),
          }}
        />
      </Card>

      {/* Key Form Modal */}
      <Modal
        title={editing ? t.keys.editKey : t.keys.createKey}
        open={modalOpen}
        onOk={newKeyValue ? () => setModalOpen(false) : handleSubmit}
        onCancel={() => setModalOpen(false)}
        okText={newKeyValue ? t.common.complete : editing ? t.common.save : t.common.create}
        cancelText={t.common.cancel}
        width={520}
        destroyOnClose
      >
        {newKeyValue ? (
          <div className="py-4">
            <Alert
              type="warning"
              showIcon
              message={t.keys.copyWarning}
              description={t.keys.copyWarningDesc}
              className="mb-4"
            />
            <div className="bg-gray-50 dark:bg-gray-800 rounded-lg p-4">
              <div className="flex items-center gap-2 mb-2">
                <KeyOutlined className="text-blue-500" />
                <Text strong>API Key</Text>
              </div>
              <Input.Search
                value={newKeyValue}
                readOnly
                enterButton={<CopyOutlined />}
                onSearch={handleCopyKey}
                className="font-mono"
              />
            </div>
          </div>
        ) : (
          <Form form={form} layout="vertical" className="mt-4">
            <Form.Item
              name="name"
              label={t.keys.fieldName}
              rules={[{ required: true, message: t.common.required }]}
            >
              <Input placeholder={t.keys.fieldNamePlaceholder} />
            </Form.Item>
            {editing && (
              <Form.Item name="enabled" label={t.keys.fieldEnabled} valuePropName="checked">
                <Switch />
              </Form.Item>
            )}
            <Form.Item
              name="kiro_accounts"
              label={t.keys.fieldKiroAccounts}
              rules={[{ required: true, message: t.common.required }]}
            >
              <Select
                mode="multiple"
                placeholder={t.keys.fieldKiroAccountsPlaceholder}
                options={accounts.map((account) => ({ label: account.name, value: account.name }))}
              />
            </Form.Item>
            <Form.Item
              name="kiro_default_account"
              label={t.keys.fieldKiroDefaultAccount}
              rules={[{ required: true, message: t.common.required }]}
            >
              <Select
                placeholder={t.keys.fieldKiroDefaultAccountPlaceholder}
                options={selectedKiroAccounts.map((account) => ({ label: account, value: account }))}
              />
            </Form.Item>
            <Form.Item
              name="suppress_reasoning"
              label={t.keys.fieldSuppressReasoning}
              valuePropName="checked"
              tooltip={t.keys.fieldSuppressReasoningTooltip}
            >
              <Switch />
            </Form.Item>
          </Form>
        )}
      </Modal>

      {/* Usage Modal */}
      {selectedKey && (
        <UsageModal
          open={usageModalOpen}
          keyId={selectedKey.id}
          keyName={selectedKey.name}
          onClose={() => {
            setUsageModalOpen(false);
            setSelectedKey(null);
          }}
        />
      )}
    </div>
  );
}
