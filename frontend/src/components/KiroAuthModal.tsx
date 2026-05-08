import { useState, useRef, useEffect } from "react";
import { Modal, Typography, Button, Tag, Space, App, Empty, Input, Select, Divider, Tabs, Form } from "antd";
import { ThunderboltOutlined, ReloadOutlined, SyncOutlined, CheckCircleOutlined, CloseCircleOutlined, ClockCircleOutlined, ImportOutlined, KeyOutlined } from "@ant-design/icons";
import {
  startKiroLogin,
  startKiroDeviceLogin,
  getKiroLoginStatus,
  completeKiroLogin,
  getKiroStatus,
  refreshKiroToken,
  importKiroLocal,
  type KiroStatus,
} from "@/services/api";
import { useT } from "@/locales";

const { Text, Link } = Typography;

type DeviceLoginMethod = "builder_id" | "organization";

interface Props {
  open: boolean;
  providerName: string;
  providerRegion?: string;
  onClose: () => void;
}

export default function KiroAuthModal({ open, providerName, providerRegion, onClose }: Props) {
  const [status, setStatus] = useState<KiroStatus | null>(null);
  const [loading, setLoading] = useState(false);
  const [loginUrl, setLoginUrl] = useState("");
  const [deviceLogin, setDeviceLogin] = useState<{ userCode: string; verifyUrl: string } | null>(null);
  const [deviceMethod, setDeviceMethod] = useState<DeviceLoginMethod>("organization");
  const [deviceStartUrl, setDeviceStartUrl] = useState("");
  const [deviceRegion, setDeviceRegion] = useState("us-east-1");
  const [loginLoading, setLoginLoading] = useState(false);
  const [deviceLoginLoading, setDeviceLoginLoading] = useState(false);
  const [refreshing, setRefreshing] = useState(false);
  const [importing, setImporting] = useState(false);
  const pollRef = useRef<ReturnType<typeof setInterval>>(undefined);
  const { message } = App.useApp();
  const t = useT();

  const fetchStatus = async () => {
    setLoading(true);
    try {
      const res = await getKiroStatus(providerName);
      setStatus(res);
    } catch {
      // Kiro provider may not be configured
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    if (open) {
      setDeviceRegion(providerRegion || "us-east-1");
      fetchStatus();
    }
    return () => {
      if (pollRef.current) clearInterval(pollRef.current);
    };
  }, [open, providerName, providerRegion]);

  const handleLogin = async () => {
    setLoginLoading(true);
    try {
      const session = await startKiroLogin(providerName);
      setLoginUrl(session.auth_url);

      // Open auth URL
      window.open(session.auth_url, "_blank");

      // Poll for completion
      pollRef.current = setInterval(async () => {
        try {
          const res = await getKiroLoginStatus(session.id, providerName);
          if (res.status === "completed") {
            clearInterval(pollRef.current);
            await completeKiroLogin(session.id, providerName);
            message.success(t.kiro.loginSuccess);
            setLoginUrl("");
            setDeviceLogin(null);
            fetchStatus();
          } else if (res.status === "error" || res.error) {
            clearInterval(pollRef.current);
            message.error(res.error || t.kiro.loginFailed);
            setLoginUrl("");
            setDeviceLogin(null);
          }
        } catch {
          clearInterval(pollRef.current);
          setLoginUrl("");
          setDeviceLogin(null);
        }
      }, 3000);
    } catch {
      message.error(t.kiro.startError);
    } finally {
      setLoginLoading(false);
    }
  };

  const handleDeviceLogin = async () => {
    const startUrl = deviceStartUrl.trim();
    if (deviceMethod === "organization" && !startUrl) {
      message.warning(t.kiro.startUrlRequired);
      return;
    }
    setDeviceLoginLoading(true);
    try {
      const session = await startKiroDeviceLogin(providerName, {
        method: deviceMethod,
        idc_region: deviceRegion.trim() || "us-east-1",
        start_url: deviceMethod === "organization" ? startUrl : undefined,
      });
      const verifyUrl = session.verification_uri_complete || session.verification_uri || "";
      setDeviceLogin({ userCode: session.user_code, verifyUrl });

      if (verifyUrl) {
        window.open(verifyUrl, "_blank");
      }

      const intervalMs = Math.max(session.interval || 3, 3) * 1000;
      pollRef.current = setInterval(async () => {
        try {
          const res = await getKiroLoginStatus(session.id, providerName);
          if (res.status === "completed") {
            clearInterval(pollRef.current);
            await completeKiroLogin(session.id, providerName);
            message.success(t.kiro.loginSuccess);
            setDeviceLogin(null);
            fetchStatus();
          } else if (res.status === "error" || res.status === "expired" || res.error) {
            clearInterval(pollRef.current);
            message.error(res.error || t.kiro.loginFailed);
            setDeviceLogin(null);
          }
        } catch {
          clearInterval(pollRef.current);
          setDeviceLogin(null);
        }
      }, intervalMs);
    } catch (err) {
      message.error(err instanceof Error ? err.message : t.kiro.startError);
    } finally {
      setDeviceLoginLoading(false);
    }
  };

  const handleRefreshToken = async () => {
    setRefreshing(true);
    try {
      const res = await refreshKiroToken(providerName);
      setStatus(res);
      message.success(t.kiro.tokenRefreshSuccess);
    } catch {
      message.error(t.kiro.tokenRefreshFailed);
    } finally {
      setRefreshing(false);
    }
  };

  const handleImportLocal = async () => {
    setImporting(true);
    try {
      await importKiroLocal(providerName);
      message.success(t.kiro.importLocalSuccess);
      fetchStatus();
    } catch {
      message.error(t.kiro.importLocalFailed);
    } finally {
      setImporting(false);
    }
  };

  const handleClose = () => {
    if (pollRef.current) clearInterval(pollRef.current);
    setLoginUrl("");
    setDeviceLogin(null);
    onClose();
  };

  return (
    <Modal
      title={
        <Space>
          <ThunderboltOutlined />
          <span>{t.kiro.title} - {providerName}</span>
        </Space>
      }
      open={open}
      onCancel={handleClose}
      footer={null}
      width={760}
      destroyOnClose
    >
      <div className="space-y-5">
        <section className="rounded-lg border border-gray-200 dark:border-gray-700 p-4">
          <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
            <div>
              <Text strong className="block">{t.kiro.currentStatus}</Text>
              <Space wrap size={[8, 8]} className="mt-2">
                {status?.has_login ? (
                  <Tag color="green" icon={<CheckCircleOutlined />}>{t.kiro.configured}</Tag>
                ) : (
                  <Tag color="red" icon={<CloseCircleOutlined />}>{t.kiro.notConfigured}</Tag>
                )}
                {status?.has_current ? (
                  <Tag color="green" icon={<CheckCircleOutlined />}>{t.kiro.valid}</Tag>
                ) : (
                  <Tag color="orange" icon={<CloseCircleOutlined />}>{t.kiro.none}</Tag>
                )}
                {status?.is_external_idp && <Tag color="blue">{t.kiro.externalIdp}</Tag>}
                {status?.expires_at && (
                  <Tag icon={<ClockCircleOutlined />}>{status.expires_at}</Tag>
                )}
              </Space>
            </div>
            <Button icon={<ReloadOutlined />} onClick={fetchStatus} loading={loading}>
              {t.common.refresh}
            </Button>
          </div>
          {loading && !status && (
            <div className="flex justify-center py-8">
              <div className="w-8 h-8 rounded-full border-2 border-blue-500 border-t-transparent animate-spin" />
            </div>
          )}
          {!loading && !status && (
            <Empty
              image={Empty.PRESENTED_IMAGE_SIMPLE}
              description={t.kiro.statusError}
              className="!my-4"
            >
              <Text type="secondary">{t.kiro.confirmConfig}</Text>
            </Empty>
          )}
        </section>

        {(loginUrl || deviceLogin) && (
          <section className="rounded-lg border border-blue-200 bg-blue-50 p-4 text-center dark:border-blue-800 dark:bg-blue-900/20">
            <div className="flex items-center justify-center gap-2 mb-3">
              <div className="w-6 h-6 rounded-full border-2 border-blue-500 border-t-transparent animate-spin" />
              <Text>{t.kiro.pendingAuth}</Text>
            </div>
            {deviceLogin ? (
              <>
                <Text type="secondary" className="block mb-2">{t.kiro.deviceCode}</Text>
                <Text code copyable className="text-lg">{deviceLogin.userCode}</Text>
                {deviceLogin.verifyUrl && (
                  <div className="mt-3">
                    <Link href={deviceLogin.verifyUrl} target="_blank" className="text-blue-600">
                      {t.kiro.openAuthPage}
                    </Link>
                  </div>
                )}
              </>
            ) : (
              <Link href={loginUrl} target="_blank" className="text-blue-600">
                {t.kiro.openAuthPage}
              </Link>
            )}
          </section>
        )}

        <section>
          <div className="mb-3">
            <Text strong>{t.kiro.loginMethods}</Text>
          </div>
          <div className="rounded-lg border border-gray-200 dark:border-gray-700 px-4 pb-4">
            <Tabs
              items={[
                {
                  key: "pkce",
                  label: t.kiro.pkceLogin,
                  children: (
                    <div className="pt-1">
                      <Button
                        type="primary"
                        block
                        icon={<ThunderboltOutlined />}
                        onClick={handleLogin}
                        loading={loginLoading}
                        disabled={!!loginUrl || !!deviceLogin}
                      >
                        {t.kiro.openAuthPage}
                      </Button>
                    </div>
                  ),
                },
                {
                  key: "device",
                  label: t.kiro.deviceLogin,
                  children: (
                    <Form layout="vertical" className="pt-1" requiredMark={false}>
                      <Form.Item label={t.kiro.deviceMethod} name="device_method" className="!mb-3">
                        <Select<DeviceLoginMethod>
                          value={deviceMethod}
                          onChange={setDeviceMethod}
                          className="w-full"
                          disabled={!!loginUrl || !!deviceLogin || deviceLoginLoading}
                          options={[
                            { label: t.kiro.deviceMethodBuilderID, value: "builder_id" },
                            { label: t.kiro.deviceMethodOrganization, value: "organization" },
                          ]}
                        />
                      </Form.Item>
                      <Form.Item label={t.kiro.idcRegion} name="idc_region" className="!mb-3">
                        <Input
                          value={deviceRegion}
                          onChange={(e) => setDeviceRegion(e.target.value)}
                          placeholder={t.kiro.idcRegionPlaceholder}
                          disabled={!!loginUrl || !!deviceLogin || deviceLoginLoading}
                        />
                      </Form.Item>
                      {deviceMethod === "organization" && (
                        <Form.Item label={t.kiro.startUrl} name="start_url" className="!mb-3">
                          <Input
                            value={deviceStartUrl}
                            onChange={(e) => setDeviceStartUrl(e.target.value)}
                            placeholder={t.kiro.startUrlPlaceholder}
                            disabled={!!loginUrl || !!deviceLogin || deviceLoginLoading}
                          />
                        </Form.Item>
                      )}
                      <Button
                        type="primary"
                        block
                        icon={<KeyOutlined />}
                        onClick={handleDeviceLogin}
                        loading={deviceLoginLoading}
                        disabled={!!loginUrl || !!deviceLogin}
                      >
                        {t.kiro.deviceLogin}
                      </Button>
                    </Form>
                  ),
                },
                {
                  key: "local",
                  label: t.kiro.localLogin,
                  children: (
                    <div className="pt-1">
                      <Button
                        type="primary"
                        block
                        icon={<ImportOutlined />}
                        onClick={handleImportLocal}
                        loading={importing}
                        disabled={!!loginUrl || !!deviceLogin}
                      >
                        {t.kiro.importLocal}
                      </Button>
                    </div>
                  ),
                },
              ]}
            />
          </div>
        </section>

        <Divider className="!my-0" />

        <section>
          <div className="mb-3">
            <Text strong>{t.kiro.maintenance}</Text>
          </div>
          <Space wrap>
            <Button
              icon={<SyncOutlined />}
              onClick={handleRefreshToken}
              loading={refreshing}
              disabled={!status?.has_login}
            >
              {t.kiro.refreshToken}
            </Button>
          </Space>
        </section>
      </div>
    </Modal>
  );
}
