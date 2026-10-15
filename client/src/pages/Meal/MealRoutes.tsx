import { Routes, Route } from 'react-router';
import Meal from './Meal';
import AddComponent from './AddComponent';

// One lazy chunk for the whole meal flow: /entries/:id and /entries/:id/add.
export default function MealRoutes() {
  return (
    <Routes>
      <Route index element={<Meal />} />
      <Route path="add" element={<AddComponent />} />
    </Routes>
  );
}
