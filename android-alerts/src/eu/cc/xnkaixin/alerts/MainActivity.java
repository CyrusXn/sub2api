package eu.cc.xnkaixin.alerts;

import android.Manifest;
import android.app.Activity;
import android.content.Intent;
import android.content.SharedPreferences;
import android.content.pm.PackageManager;
import android.graphics.Color;
import android.os.Build;
import android.os.Bundle;
import android.os.Handler;
import android.provider.Settings;
import android.text.InputType;
import android.view.WindowInsets;
import android.widget.Button;
import android.widget.EditText;
import android.widget.LinearLayout;
import android.widget.ScrollView;
import android.widget.TextView;

import java.text.DateFormat;
import java.util.Date;

public final class MainActivity extends Activity implements SharedPreferences.OnSharedPreferenceChangeListener {
    private static final int REQUEST_NOTIFICATIONS = 41;
    private final Handler main = new Handler(android.os.Looper.getMainLooper());
    private AlertSettings settings;
    private EditText site;
    private EditText token;
    private TextView status;
    private LinearLayout form;

    @Override public void onCreate(Bundle state) {
        super.onCreate(state);
        settings = new AlertSettings(this);
        AlertNotifications.createChannels(this);
        ScrollView scroll = new ScrollView(this);
        scroll.setBackgroundColor(Color.rgb(19, 23, 29));
        if (Build.VERSION.SDK_INT >= 30) {
            scroll.setOnApplyWindowInsetsListener((view, insets) -> {
                android.graphics.Insets bars = insets.getInsets(WindowInsets.Type.systemBars() | WindowInsets.Type.displayCutout());
                view.setPadding(bars.left, bars.top, bars.right, bars.bottom);
                return insets;
            });
        }
        form = new LinearLayout(this);
        form.setOrientation(LinearLayout.VERTICAL);
        form.setPadding(dp(22), dp(26), dp(22), dp(24));
        scroll.addView(form);
        setContentView(scroll);

        TextView heading = label("Sub2API 告警", 28);
        heading.setTypeface(null, android.graphics.Typeface.BOLD);
        label("上游余额与倍率变化", 15);
        status = label("", 15);
        site = field("HTTPS 站点", InputType.TYPE_CLASS_TEXT | InputType.TYPE_TEXT_VARIATION_URI);
        site.setText(settings.site());
        token = field(settings.hasToken() ? "已保存口令，修改时输入" : "手机告警专用口令",
                InputType.TYPE_CLASS_TEXT | InputType.TYPE_TEXT_VARIATION_PASSWORD);
        token.setSaveEnabled(false);
        button("保存并开始监控", this::saveAndStart);
        button("发送测试通知", this::testNotification);
        button("系统通知设置", this::openNotificationSettings);
        button("停止监控", () -> {
            stopService(new Intent(this, AlertService.class));
            settings.prefs.edit().putString("status", "监控已停止").apply();
        });
        label("请在系统通知设置中允许弹出和显示锁屏内容。重启手机或强制停止应用后，请重新开启监控。", 14);
        refreshStatus();
    }

    @Override protected void onStart() {
        super.onStart();
        settings.prefs.registerOnSharedPreferenceChangeListener(this);
        refreshStatus();
    }

    @Override protected void onStop() {
        settings.prefs.unregisterOnSharedPreferenceChangeListener(this);
        super.onStop();
    }

    @Override public void onSharedPreferenceChanged(SharedPreferences preferences, String key) {
        refreshStatus();
    }

    private void refreshStatus() {
        String text = settings.prefs.getString("status", "尚未连接");
        long lastSuccess = settings.prefs.getLong("last_success", 0);
        if (lastSuccess > 0) {
            text += "\n上次连接 " + DateFormat.getDateTimeInstance().format(new Date(lastSuccess));
        }
        String lastAlert = settings.prefs.getString("last_alert", "");
        if (!lastAlert.isEmpty()) text += "\n\n最近告警\n" + lastAlert;
        status.setText(text);
    }

    private void saveAndStart() {
        if (!ensureNotificationPermission()) return;
        if (!AlertNotifications.canNotify(this, AlertNotifications.BALANCE)
                || !AlertNotifications.canNotify(this, AlertNotifications.RATE)) {
            status.setText("请在系统设置中开启余额和倍率的通知类别");
            return;
        }
        String address;
        try {
            address = AlertSettings.validateSite(site.getText().toString().trim());
        } catch (IllegalArgumentException error) {
            status.setText("请输入有效的 HTTPS 站点地址");
            return;
        }
        String secret = token.getText().toString().trim();
        try {
            synchronized (settings.prefs) {
                settings.save(address, secret);
            }
        } catch (IllegalArgumentException error) {
            status.setText(error.getMessage());
            return;
        } catch (Exception error) {
            status.setText("系统无法保存口令，请重试");
            return;
        }
        token.setText("");
        token.setHint("已保存口令，修改时输入");
        settings.prefs.edit().putString("status", "正在连接").apply();
        startForegroundService(new Intent(this, AlertService.class));
    }

    private void testNotification() {
        if (!ensureNotificationPermission()) return;
        if (!AlertNotifications.canNotify(this, AlertNotifications.BALANCE)) {
            status.setText("请开启余额不足通知类别");
            return;
        }
        status.setText("将在 5 秒后发送测试通知，可以现在锁屏");
        main.postDelayed(() -> AlertNotifications.show(getApplicationContext(), "test:" + System.currentTimeMillis(),
                "low_balance", "余额不足 · 测试", "这是一条本机测试。可检查锁屏、下拉栏、声音和振动。"), 5_000);
    }

    private boolean ensureNotificationPermission() {
        if (Build.VERSION.SDK_INT >= 33 && checkSelfPermission(Manifest.permission.POST_NOTIFICATIONS) != PackageManager.PERMISSION_GRANTED) {
            requestPermissions(new String[]{Manifest.permission.POST_NOTIFICATIONS}, REQUEST_NOTIFICATIONS);
            status.setText("请允许通知，授权后再点击一次按钮");
            return false;
        }
        if (!AlertNotifications.canNotify(this, null)) {
            openNotificationSettings();
            return false;
        }
        return true;
    }

    private void openNotificationSettings() {
        startActivity(new Intent(Settings.ACTION_APP_NOTIFICATION_SETTINGS).putExtra(Settings.EXTRA_APP_PACKAGE, getPackageName()));
    }

    private TextView label(String text, int size) {
        TextView label = new TextView(this);
        label.setText(text);
        label.setTextSize(size);
        label.setTextColor(0xFFE3EBF5);
        label.setPadding(0, dp(7), 0, dp(8));
        form.addView(label);
        return label;
    }

    private EditText field(String hint, int inputType) {
        EditText field = new EditText(this);
        field.setHint(hint);
        field.setContentDescription(hint);
        field.setSingleLine(true);
        field.setInputType(inputType);
        form.addView(field, new LinearLayout.LayoutParams(-1, -2));
        return field;
    }

    private void button(String text, Runnable action) {
        Button button = new Button(this);
        button.setText(text);
        button.setOnClickListener(view -> action.run());
        form.addView(button, new LinearLayout.LayoutParams(-1, -2));
    }

    private int dp(int value) {
        return Math.round(value * getResources().getDisplayMetrics().density);
    }
}
