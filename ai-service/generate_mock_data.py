import pandas as pd
import random
from datetime import datetime, timedelta

def generate_usage_logs():
    apps = ['Instagram', 'YouTube', 'Notion', 'VS Code', 'Chrome']
    data = []
    current_time = datetime.now()

    for i in range(200):
        app = random.choice(apps)
        duration = random.randint(1, 60)
        unlocks = random.randint(1,5)

        # Basic logic: Social media apps that are used for more than 15 minutes are flagged as distracting
        # Basic logic: Social apps used for more than 15 minutes are generally distracting
        is_distracted = 1 if app in ['Instagram', 'YouTube', 'WhatsApp'] and duration > 15 else 0

        # Introduce 15% real-world noise (randomly flip the flag)
        if random.random() < 0.15:
            is_distracted = 1 - is_distracted

        data.append({
            'timestamp': (current_time - timedelta(minutes = i*45)).strftime('%Y-%m-%d %H:%M:%S'),
            'app_name': app,
            'session_duration_minutes': duration,
            'unlock_count': unlocks,
            'distraction_flag': is_distracted
        })

        df = pd.DataFrame(data)
        df.to_csv('synthetic_usage_logs.csv', index = False)
        print("Dataset generated successfully. Found", len(df), "records")


if __name__ == "__main__":
    generate_usage_logs()