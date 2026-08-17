import pandas as pd
import joblib
from sklearn.model_selection import train_test_split
from sklearn.ensemble import RandomForestClassifier
from sklearn.metrics import accuracy_score, classification_report


def train_distraction_model():
    try:
        df = pd.read_csv('synthetic_usage_logs.csv')
    except FileNotFoundError:
        print("Error: synthetic_usage_logs.csv not found.")
        return

    # --- FEATURE ENGINEERING ---
    # 1. Create a ratio feature: Avg minutes spent per individual unlock
    df['duration_per_unlock'] = df['session_duration_minutes'] / (df['unlock_count'] + 1)  # Prevent division by zero

    # 2. Convert categorical 'app_name' into numerical columns (One-Hot Encoding)
    # This creates columns like app_name_Instagram, app_name_VS Code with 1s and 0s
    df_encoded = pd.get_dummies(df, columns=['app_name'], drop_first=False)

    # 3. Define the updated feature list (excluding target and timestamp)
    feature_columns = [col for col in df_encoded.columns if col not in ['timestamp', 'distraction_flag']]

    x = df_encoded[feature_columns]
    y = df_encoded['distraction_flag']

    # Split into training and testing sets
    x_train, x_test, y_train, y_test = train_test_split(x, y, test_size=0.2, random_state=42)

    # Train the Random Forest Classifier
    print("Training the engineered Random Forest model...")
    model = RandomForestClassifier(n_estimators=150, max_depth=8, random_state=42)
    model.fit(x_train, y_train)

    # Evaluate performance
    predictions = model.predict(x_test)
    accuracy = accuracy_score(y_test, predictions)
    print(f"\nModel Training Complete!")
    print(f"Engineered Accuracy Score: {accuracy * 100:.2f}%")
    print("\nClassification Report:")
    print(classification_report(y_test, predictions))

    # Save the optimized model
    joblib.dump(model, 'distraction_classifier.joblib')
    print("Optimized model successfully exported.")


if __name__ == "__main__":
    train_distraction_model()
